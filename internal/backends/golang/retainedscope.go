package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// retainedScope owns a whole-scope compilation. This form is necessary for
// multi-package coverpkg: cmd/go compiles the complete selected coverage roster
// into every test main. Its work directory also retains executables with equal
// basenames without -o directory's collision refusal. Close after all package
// attempts; individual Artifact.Close calls do not remove this shared work.
type retainedScope struct {
	Packages map[string]retainedBuildResult
	Stream   *streamState
	Stderr   boundedBuffer
	Err      error
	work     string
	keepWork bool
}

func (s *retainedScope) Close() error {
	var errs []error
	for _, p := range s.Packages {
		if p.Artifact != nil {
			errs = append(errs, p.Artifact.Close())
		}
	}
	if s.work != "" && !s.keepWork {
		errs = append(errs, os.RemoveAll(s.work))
	}
	return errors.Join(errs...)
}

// buildRetainedScope builds exactly packages together, preserving Go's shared
// coverage test-main semantics and its per-package failure diagnostics. A
// nonzero compiler exit refuses native binding, but a successful sibling's
// artifact still executes ordinarily: a failed package must not swallow the
// remainder of Go's scope.
func buildRetainedScope(ctx context.Context, n *NormalizedInvocation, packages []string, hooks retainedProcessHooks) (s *retainedScope) {
	s = &retainedScope{Packages: map[string]retainedBuildResult{}}
	flags, err := retainedGOFLAGS(n)
	if err != nil {
		s.Err = err
		return
	}
	s.keepWork = flags.keepWork
	if len(packages) == 0 {
		s.Err = errors.New("retained compilation has no packages")
		return
	}
	publicPaths, err := retainedScopeCompanions(n, packages, flags)
	if err != nil {
		s.Err = err
		return
	}
	if err := ensureTelemetryOwned(n.Env, n.TelemetrySource); err != nil {
		s.Err = err
		return
	}
	args := retainedBuildArgs(n, packages, os.DevNull, true)
	cmd, err := ownedRunner.Command(ctx, n.Dir, witnessEnvOf(n), args...)
	if err != nil {
		s.Err = err
		return
	}
	cmd.Stderr = writerFunc(func(p []byte) (int, error) { s.Stderr.write(string(p)); return len(p), nil })
	read, write, err := os.Pipe()
	if err != nil {
		s.Err = err
		return
	}
	defer read.Close()
	cmd.Stdout = write
	finish, err := startRetainedCommand(ctx, cmd, "build", hooks)
	_ = write.Close()
	if err != nil {
		s.Err = err
		return
	}
	defer finish.Close()
	parsed := make(chan retainedScopeStream, 1)
	go func() { parsed <- parseRetainedScopeStream(read) }()
	buildStream := <-parsed
	s.Stream = buildStream.state
	s.Err = finish.Wait()
	if finish.ownershipErr != nil {
		s.Stream.malformed = "retained compilation ownership: " + finish.ownershipErr.Error() + "; " + s.Stream.malformed
	}
	s.work = retainedWorkDirectory(s.Stream.pkgOutput.b.String() + "\n" + s.Stderr.b.String())
	if s.Stream.malformed != "" {
		s.Err = errors.Join(s.Err, fmt.Errorf("invalid compilation stream: %s", s.Stream.malformed))
		return
	}
	commandErr := s.Err
	if s.work == "" {
		s.Err = errors.New("scope compilation did not retain its WORK directory")
		return
	}
	requested := map[string]bool{}
	for _, pkg := range packages {
		requested[pkg] = true
	}
	err = filepath.WalkDir(s.work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "_testmain.go" {
			return nil
		}
		pkg, err := retainedTestMainPackage(path)
		if err != nil {
			return err
		}
		if !requested[pkg] {
			return fmt.Errorf("retained test main names unselected package %q", pkg)
		}
		if _, exists := s.Packages[pkg]; exists {
			return fmt.Errorf("multiple retained test mains for %s", pkg)
		}
		if buildStream.failed[pkg] || buildStream.failed[pkg+".test"] || buildStream.failed[pkg+"_test"] {
			s.Packages[pkg] = retainedBuildResult{Err: commandErr, State: cmd.ProcessState, Stream: s.Stream}
			return nil
		}
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			return err
		}
		binary := ""
		for _, entry := range entries {
			if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".test") || strings.HasSuffix(entry.Name(), ".test.exe")) {
				if binary != "" {
					return fmt.Errorf("multiple test executables beside %s", path)
				}
				binary = filepath.Join(filepath.Dir(path), entry.Name())
			}
		}
		if binary == "" {
			if commandErr != nil {
				s.Packages[pkg] = retainedBuildResult{Err: commandErr, State: cmd.ProcessState, Stream: s.Stream}
				return nil
			}
			return fmt.Errorf("compilation produced no executable beside %s", path)
		}
		info, err := verifyRetainedExecutable(binary)
		if err != nil {
			return err
		}
		// Copy out of WORK: attempts own a stable private artifact path while
		// metadata stays in the scope's shared compiler work directory.
		root, err := os.MkdirTemp("", "stipulator-retained-*")
		if err != nil {
			return err
		}
		a := &retainedArtifact{Package: pkg, Path: filepath.Join(root, filepath.Base(binary)), root: root, work: s.work, flags: flags, sharedWork: true, BindingError: commandErr, coveragePackages: packages, publicPath: publicPaths[pkg]}
		in, err := os.Open(binary)
		if err != nil {
			a.Close()
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(a.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			a.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		err = errors.Join(copyErr, out.Close())
		if err != nil {
			a.Close()
			return err
		}
		s.Packages[pkg] = retainedBuildResult{Artifact: a, State: cmd.ProcessState}
		return nil
	})
	if err != nil {
		s.Err = err
		return
	}
	for _, pkg := range packages {
		if _, ok := s.Packages[pkg]; ok {
			continue
		}
		known := false
		for _, p := range n.selectedPackages {
			if p.ImportPath == pkg && p.Error == nil && len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
				known = true
				break
			}
		}
		if !known || !buildStream.completed[pkg] {
			err := commandErr
			if err == nil {
				err = fmt.Errorf("scope compilation produced no artifact for %s", pkg)
				s.Err = errors.Join(s.Err, err)
			}
			s.Packages[pkg] = retainedBuildResult{Err: err, State: cmd.ProcessState, Stream: s.Stream}
			continue
		}
		root, err := os.MkdirTemp("", "stipulator-retained-*")
		if err != nil {
			s.Err = err
			return
		}
		s.Packages[pkg] = retainedBuildResult{NoTestFiles: true, State: cmd.ProcessState, Artifact: &retainedArtifact{Package: pkg, root: root, work: s.work, flags: flags, sharedWork: true, BindingError: commandErr, coveragePackages: packages}}
	}
	return
}

type retainedScopeStream struct {
	state             *streamState
	failed, completed map[string]bool
}

// Compiler events retain their package attribution; a build-fail of an
// internal/external test variant belongs to its original package. This parser
// consumes build-only JSON, never a test-process stream or witness outcome.
func parseRetainedScopeStream(r io.Reader) retainedScopeStream {
	s := retainedScopeStream{state: &streamState{}, failed: map[string]bool{}, completed: map[string]bool{}}
	dec := json.NewDecoder(r)
	for {
		var e struct {
			testEvent
			ImportPath, Package string
		}
		if err := dec.Decode(&e); err != nil {
			if err != io.EOF {
				s.state.malformed = err.Error()
				_, _ = io.Copy(io.Discard, r)
			}
			return s
		}
		s.state.events++
		pkg := e.Package
		if pkg == "" {
			pkg = e.ImportPath
		}
		pkg, _, _ = strings.Cut(pkg, " [")
		switch e.Action {
		case "build-output", "output":
			s.state.pkgOutput.write(e.Output)
		case "build-fail", "fail":
			s.failed[pkg] = true
			s.state.failedBuild = true
		case "pass", "skip":
			s.completed[pkg] = true
		}
	}
}

// retainedTestMainPackage reads the generated main's explicit attribution,
// rather than guessing from a package basename or test import aliases.
func retainedTestMainPackage(path string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return "", err
	}
	pkg := ""
	ast.Inspect(f, func(node ast.Node) bool {
		a, ok := node.(*ast.AssignStmt)
		if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
			return true
		}
		s, ok := a.Lhs[0].(*ast.SelectorExpr)
		if !ok || s.Sel.Name != "ImportPath" {
			return true
		}
		x, ok := s.X.(*ast.Ident)
		if !ok || x.Name != "testdeps" {
			return true
		}
		v, ok := a.Rhs[0].(*ast.BasicLit)
		if !ok || v.Kind != token.STRING {
			return true
		}
		pkg, err = strconv.Unquote(v.Value)
		return false
	})
	if err != nil {
		return "", err
	}
	if pkg == "" {
		return "", fmt.Errorf("generated test main %s has no package attribution", path)
	}
	return pkg, nil
}
