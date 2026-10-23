package golang

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// retainedProcessHooks accounts for the sequential compiler, utility and binary
// roots belonging to one package slot. Reaped runs immediately after Wait;
// output conversion is in-process and registers no root of its own.
type retainedProcessHooks struct {
	Spawned func(stage string, pid int)
	Reaped  func(stage string, pid int, state *os.ProcessState)
}

// retainedArtifact owns its private executable and any retained coverage build
// metadata. Close only after every attempt using it has finished.
type retainedArtifact struct {
	Package    string
	Path       string
	root       string
	work       string
	flags      retainedFlags
	sharedWork bool
	// BindingError refuses a native compilation binding when this artifact
	// came from a successful package branch of a failed whole-scope command.
	// Its ordinary execution remains valid; the failed sibling stays failed.
	BindingError error
	// coveragePackages is the explicit compiler scope, not the instrumented
	// dependency closure. Only these packages contribute auxiliary metadata.
	coveragePackages []string
	publicPath       string
}

func (a *retainedArtifact) Close() error {
	if a == nil {
		return nil
	}
	var err error
	if a.work != "" && !a.sharedWork && !a.flags.keepWork {
		err = os.RemoveAll(a.work)
	}
	return errors.Join(err, os.RemoveAll(a.root))
}

type retainedBuildResult struct {
	Artifact    *retainedArtifact
	NoTestFiles bool
	Stream      *streamState
	Stderr      boundedBuffer
	State       *os.ProcessState
	Err         error
}

func retainedBuildArgs(n *NormalizedInvocation, packages []string, artifact string, keepWork bool) []string {
	args := []string{"test", "-c", "-json", "-timeout=0", "-o", artifact}
	if keepWork {
		args = append(args, "-work")
	}
	args = append(args, buildFlags(n.Race, n.Tags, n.ModuleMode, buildPGO(treeRoot(n), n.PGO))...)
	args = append(args, linkerBuildFlags(n.LinkerAssignments)...)
	return append(args, packages...)
}

func retainedWorkDirectory(output string) string {
	for line := range strings.Lines(output) {
		if !strings.HasSuffix(line, "\n") || !strings.HasPrefix(line, "WORK=") {
			continue
		}
		path := strings.TrimSuffix(strings.TrimPrefix(line, "WORK="), "\n")
		if filepath.IsAbs(path) && strings.HasPrefix(filepath.Base(path), "go-build") {
			return path
		}
	}
	return ""
}

// buildRetained compiles and vets under the complete compilation description.
// A successful command alone is not an artifact: the private output must be a
// nonempty regular executable. Only Go's no-test-files answer admits absence.
func buildRetained(ctx context.Context, n *NormalizedInvocation, pkg string, hooks retainedProcessHooks) (r retainedBuildResult) {
	flags, err := retainedGOFLAGS(n)
	if err != nil {
		r.Err = err
		return
	}
	root, err := os.MkdirTemp("", "stipulator-retained-*")
	if err != nil {
		r.Err = err
		return
	}
	a := &retainedArtifact{Package: pkg, root: root, flags: flags, Path: filepath.Join(root, "package.test"), coveragePackages: []string{pkg}}
	a.publicPath = retainedCompanionPath(n, pkg, flags)
	if runtime.GOOS == "windows" {
		a.Path += ".exe"
	}
	defer func() {
		if r.Artifact == nil {
			_ = a.Close()
		}
	}()
	if err = ensureTelemetryOwned(n.Env, n.TelemetrySource); err != nil {
		r.Err = err
		return
	}
	cmd, err := ownedRunner.Command(ctx, n.Dir, witnessEnvOf(n), retainedBuildArgs(n, []string{pkg}, a.Path, flags.cover)...)
	if err != nil {
		r.Err = err
		return
	}
	var workLine strings.Builder
	cmd.Stderr = writerFunc(func(p []byte) (int, error) {
		r.Stderr.write(string(p))
		// WORK is cmd/go's first stderr line with -work. Retain it
		// independently of diagnostic truncation, without growing a log.
		if flags.cover && a.work == "" && workLine.Len() < 32<<10 {
			workLine.Write(p)
			a.work = retainedWorkDirectory(workLine.String())
		}
		return len(p), nil
	})
	read, write, err := os.Pipe()
	if err != nil {
		r.Err = err
		return
	}
	defer read.Close()
	cmd.Stdout = write
	finish, err := startRetainedCommand(ctx, cmd, "build", hooks)
	_ = write.Close()
	if err != nil {
		r.Err = err
		return
	}
	defer finish.Close()
	parsed := make(chan *streamState, 1)
	go func() { parsed <- parseTestStream(n.Name, pkg, read, nil) }()
	r.Stream = <-parsed
	r.Err = finish.Wait()
	r.State = cmd.ProcessState
	if finish.ownershipErr != nil {
		r.Stream.malformed = "retained compilation ownership: " + finish.ownershipErr.Error() + "; " + r.Stream.malformed
	}
	if flags.cover && a.work == "" {
		// With build JSON enabled the background shell writes WORK as a
		// build-output event; older toolchains put it on stderr above.
		a.work = retainedWorkDirectory(r.Stream.pkgOutput.b.String())
	}
	if r.Err != nil {
		return
	}
	if r.Stream.malformed != "" || r.Stream.failedBuild {
		r.Err = fmt.Errorf("retained compilation returned an invalid or failed build stream: %s", r.Stream.malformed)
		return
	}
	_, err = verifyRetainedExecutable(a.Path)
	noTests := r.Stream.terminal == "skip" && strings.Contains(r.Stream.pkgOutput.b.String(), "[no test files]")
	// With coverage, Go prints static coverage instead of "no test files"
	// and terminates with pass. Discovery's original-package inventory
	// distinguishes that build-only package from a missing test artifact.
	if flags.cover && r.Stream.terminal == "pass" {
		for _, p := range n.selectedPackages {
			if p.ImportPath == pkg && p.Error == nil && len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
				noTests = true
			}
		}
	}
	if errors.Is(err, os.ErrNotExist) && noTests {
		r.NoTestFiles = true
		// Coverage's build-only package still owns static metadata.
		if flags.cover {
			a.Path = ""
			r.Artifact = a
		}
		return
	}
	if err != nil {
		r.Err = fmt.Errorf("retained compilation produced no test artifact: %w", err)
		return
	}
	r.Artifact = a
	return
}

func verifyRetainedExecutable(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("retained output is not a nonempty regular executable: %s", path)
	}
	return info, nil
}

// retainedCommand holds the leader waitable while descendants can still own a
// stream, then sweeps before the actual Wait releases its identity. Cancellation
// is single-owner even when os/exec's watcher and the drainage watcher race.
type retainedCommand struct {
	cmd          *exec.Cmd
	stage        string
	hooks        retainedProcessHooks
	signals      sync.Mutex
	finished     bool
	waitStarted  bool
	cancelOnce   sync.Once
	cancelErr    error
	cancel       func() error
	stop         func() bool
	cancelDone   chan struct{}
	exitOnce     sync.Once
	exitErr      error
	waitOnce     sync.Once
	waitErr      error
	reapOnce     sync.Once
	reapErr      error
	ownershipErr error
}

func startRetainedCommand(ctx context.Context, cmd *exec.Cmd, stage string, hooks retainedProcessHooks) (*retainedCommand, error) {
	p := &retainedCommand{cmd: cmd, stage: stage, hooks: hooks, cancelDone: make(chan struct{})}
	cancel := cmd.Cancel
	p.cancel = cancel
	cmd.Cancel = func() error {
		p.signals.Lock()
		defer p.signals.Unlock()
		if p.finished {
			return os.ErrProcessDone
		}
		p.cancelOnce.Do(func() {
			if cancel == nil {
				p.cancelErr = cmd.Process.Kill()
			} else {
				p.cancelErr = cancel()
			}
		})
		return p.cancelErr
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if hooks.Spawned != nil {
		hooks.Spawned(stage, cmd.Process.Pid)
	}
	p.stop = context.AfterFunc(ctx, func() { defer close(p.cancelDone); _ = cmd.Cancel() })
	return p, nil
}

// Exited observes termination without reaping. Until Wait's final sweep the
// leader remains a waitable child, pinning the numeric process-group identity.
func (p *retainedCommand) Exited() error {
	p.exitOnce.Do(func() { p.exitErr = retainedAwaitExit(p) })
	return p.exitErr
}

func (p *retainedCommand) Wait() error {
	p.waitOnce.Do(func() {
		p.signals.Lock()
		p.waitStarted = true
		p.signals.Unlock()
		if !retainedCanObserveExit {
			if !p.stop() {
				<-p.cancelDone
			}
			p.waitErr = p.reap()
			return
		}
		ownerErr := p.Exited()
		p.signals.Lock()
		p.finished = true
		var sweepErr error
		if ownerErr == nil {
			sweepErr = retainedSweep(p.cmd, p.cancel)
		} else {
			_ = p.cmd.Process.Kill()
		}
		p.signals.Unlock()
		p.stop()
		err := p.reap()
		p.ownershipErr = errors.Join(ownerErr, sweepErr)
		p.waitErr = errors.Join(p.ownershipErr, err)
	})
	return p.waitErr
}

func (p *retainedCommand) reap() error {
	p.reapOnce.Do(func() {
		p.reapErr = p.cmd.Wait()
		p.signals.Lock()
		p.finished = true
		p.signals.Unlock()
		if p.hooks.Reaped != nil {
			p.hooks.Reaped(p.stage, p.cmd.Process.Pid, p.cmd.ProcessState)
		}
	})
	return p.reapErr
}

func (p *retainedCommand) Close() { _ = p.Wait() }

// Quit requests an envelope dump while retaining the process identity. It is
// nonblocking; the caller owns the grace and can escalate through cancellation.
func (p *retainedCommand) Quit() bool {
	p.signals.Lock()
	defer p.signals.Unlock()
	if p.finished {
		return true
	}
	if !retainedCanObserveExit && p.waitStarted {
		return false
	}
	return retainedQuit(p.cmd)
}

// retainedCoverageProfile is invocation-owned: initialize once before package
// work, merge each package's fragment, close after all slots finish. Go's text
// profile concatenates fragments (it does not sum their counters).
type retainedCoverageProfile struct {
	mu   sync.Mutex
	file *os.File
	mode string
}

func openRetainedCoverage(n *NormalizedInvocation) (*retainedCoverageProfile, error) {
	flags, err := retainedGOFLAGS(n)
	if err != nil {
		return nil, err
	}
	name := flags.values["coverprofile"]
	if name == "" {
		return nil, nil
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(flags.outputDir(n), name)
	}
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err = fmt.Fprintf(f, "mode: %s\n", flags.coverMode); err != nil {
		f.Close()
		return nil, err
	}
	return &retainedCoverageProfile{file: f, mode: flags.coverMode}, nil
}

func (p *retainedCoverageProfile) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.file.Close()
}

func (p *retainedCoverageProfile) Merge(path string) error {
	if p == nil {
		return nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // A failed binary need not flush.
	if err != nil {
		return err
	}
	defer f.Close()
	header := "mode: " + p.mode + "\n"
	buf := make([]byte, len(header))
	n, err := io.ReadFull(f, buf)
	if n == 0 && err == io.EOF {
		return nil
	}
	if err != nil || string(buf) != header {
		return fmt.Errorf("test wrote malformed coverage profile %s: header %q, expected %q", path, buf[:n], header)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err = io.Copy(p.file, f)
	return err
}

// retainedTool runs coverage utilities through the same runner and package
// accounting boundary as the compiler and retained binary.
func retainedTool(ctx context.Context, n *NormalizedInvocation, hooks retainedProcessHooks, args ...string) ([]byte, error) {
	if err := ensureTelemetryOwned(n.Env, n.TelemetrySource); err != nil {
		return nil, err
	}
	cmd, err := ownedRunner.Command(ctx, n.Dir, witnessEnvOf(n), args...)
	if err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	var stderr boundedBuffer
	cmd.Stdout = write
	cmd.Stderr = writerFunc(func(p []byte) (int, error) { stderr.write(string(p)); return len(p), nil })
	finish, err := startRetainedCommand(ctx, cmd, "coverage", hooks)
	_ = write.Close()
	if err != nil {
		return nil, err
	}
	defer finish.Close()
	out, readErr := io.ReadAll(read)
	if err = errors.Join(readErr, finish.Wait()); err != nil {
		return nil, fmt.Errorf("coverage utility: %w: %s", err, stderr.b.String())
	}
	return out, nil
}
