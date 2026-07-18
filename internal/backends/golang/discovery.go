package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// ObligationKind classifies one suite obligation the Go backend defines
// for a complete suite (REQ-go-policy-complete): the package itself (its
// build, init, TestMain, and exit behavior), a named test, an executable
// example, a fuzz target's seed replay, and each committed seed file.
type ObligationKind string

const (
	// ObligationPackage is a selected package: its build, init, TestMain,
	// and exit semantics are obligations even when it has no runnable
	// test.
	ObligationPackage ObligationKind = "package"
	// ObligationTest is a named top-level Test function, internal or
	// external test package alike.
	ObligationTest ObligationKind = "test"
	// ObligationExample is an executable example — one with an output
	// comment, which `go test` runs.
	ObligationExample ObligationKind = "example"
	// ObligationFuzz is a fuzz target's deterministic seed replay in the
	// ordinary run (REQ-go-fuzz-exploration).
	ObligationFuzz ObligationKind = "fuzz"
	// ObligationSeed is one committed seed-corpus file under
	// testdata/fuzz/<target>/.
	ObligationSeed ObligationKind = "seed"
)

// Obligation is one backend-defined suite obligation. Name is empty for
// package obligations, the function name for tests, examples, and fuzz
// targets, and "<FuzzTarget>/<file>" for committed seeds.
type Obligation struct {
	Kind    ObligationKind
	Package string
	Name    string
}

// ID is the backend-scoped obligation identity carried by
// ObligationReport messages.
func (o Obligation) ID() string {
	if o.Name == "" {
		return string(o.Kind) + ":" + o.Package
	}
	return string(o.Kind) + ":" + o.Package + "." + o.Name
}

// DiscoverInvocation enumerates the complete obligation set one normalized
// invocation selects: every package its patterns match under its build
// selection, with the named tests, executable examples, fuzz targets, and
// committed seeds of each — external test packages folded to their subject
// package. The package listing runs inside an owned, cancellable process
// boundary (REQ-go-owned-processes); enumeration parses the listed test
// sources in-process, spawning nothing. A package that fails to list still
// yields its package obligation: its build failure is part of the suite,
// and execution — not discovery — is where it surfaces. Discovery also
// records each listed package's directory on the invocation (PkgDirs):
// the executor's observation bracket must be captured before the package's
// process spawns, so the directory has to be resolved ahead of execution —
// a post-run resolution could not declare what the run was allowed to read.
func DiscoverInvocation(ctx context.Context, n *NormalizedInvocation) ([]Obligation, error) {
	pkgs, err := listPackages(ctx, n)
	if err != nil {
		return nil, err
	}
	n.PkgDirs = make(map[string]string, len(pkgs))
	for _, p := range pkgs {
		if p.Dir != "" {
			n.PkgDirs[p.ImportPath] = p.Dir
		}
	}
	seen := map[string]bool{}
	var out []Obligation
	add := func(o Obligation) {
		if !seen[o.ID()] {
			seen[o.ID()] = true
			out = append(out, o)
		}
	}
	for _, p := range pkgs {
		add(Obligation{Kind: ObligationPackage, Package: p.ImportPath})
		if p.Dir == "" {
			continue
		}
		files := make([]string, 0, len(p.TestGoFiles)+len(p.XTestGoFiles))
		files = append(files, p.TestGoFiles...)
		files = append(files, p.XTestGoFiles...)
		fset := token.NewFileSet()
		var parsed []*ast.File
		for _, f := range files {
			// A test file that fails to parse is a build failure of the
			// package obligation already recorded; enumeration keeps what
			// it can.
			af, err := parser.ParseFile(fset, filepath.Join(p.Dir, f), nil, parser.ParseComments)
			if err != nil {
				continue
			}
			parsed = append(parsed, af)
			for _, d := range af.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				switch {
				case syntacticTestFunc(af, fd, "Test", "T"):
					add(Obligation{Kind: ObligationTest, Package: p.ImportPath, Name: fd.Name.Name})
				case syntacticTestFunc(af, fd, "Fuzz", "F"):
					add(Obligation{Kind: ObligationFuzz, Package: p.ImportPath, Name: fd.Name.Name})
					for _, seed := range committedSeeds(p.Dir, fd.Name.Name) {
						add(Obligation{Kind: ObligationSeed, Package: p.ImportPath, Name: fd.Name.Name + "/" + seed})
					}
				}
			}
		}
		// Executable examples only: an example without an output comment
		// is compiled, never run, so it carries no run obligation.
		for _, ex := range doc.Examples(parsed...) {
			if ex.Output == "" && !ex.EmptyOutput {
				continue
			}
			name := "Example"
			if ex.Name != "" {
				name += ex.Name
			}
			add(Obligation{Kind: ObligationExample, Package: p.ImportPath, Name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// listedPackage is the subset of `go list -json` output discovery reads.
type listedPackage struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
}

// listPackages lists the invocation's selected packages under its build
// selection through an owned, cancellable process boundary.
func listPackages(ctx context.Context, n *NormalizedInvocation) ([]listedPackage, error) {
	args := []string{"list", "-e", "-json=ImportPath,Dir,TestGoFiles,XTestGoFiles"}
	if len(n.Tags) > 0 {
		args = append(args, "-tags="+strings.Join(n.Tags, ","))
	}
	if flag := moduleModeFlag(n.ModuleMode); flag != "" {
		args = append(args, flag)
	}
	// Patterns are statically validated to never be flag-shaped, so they
	// append directly.
	args = append(args, n.Packages...)
	cmd := commandContext(ctx, "go", args...)
	cmd.Dir = n.Dir
	cmd.Env = n.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(&stdout)
	for dec.More() {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("parsing go list output for %q: %w", n.Name, err)
		}
		if p.ImportPath != "" {
			pkgs = append(pkgs, p)
		}
	}
	if len(pkgs) == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("go list for invocation %q: %v: %s", n.Name, runErr, stderr.String())
		}
		return nil, fmt.Errorf("invocation %q selects no packages", n.Name)
	}
	return pkgs, nil
}

// committedSeeds returns the committed seed-corpus file names of one fuzz
// target, sorted.
func committedSeeds(pkgDir, target string) []string {
	entries, err := os.ReadDir(filepath.Join(pkgDir, "testdata", "fuzz", target))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// syntacticTestFunc reports whether fd is a top-level test-shaped function
// — prefix-named per go test's rule (the rune after the prefix must not be
// lowercase) with exactly one parameter of type *<testing>.<handle>, where
// <testing> is the file's name for the "testing" import (a dot import
// yields the bare handle type). Discovery is syntax-only by design: it
// enumerates what `go test` would select without loading types, so the
// obligation set exists even for packages whose full type-check fails.
func syntacticTestFunc(file *ast.File, fd *ast.FuncDecl, prefix, handle string) bool {
	name := fd.Name.Name
	if !strings.HasPrefix(name, prefix) || fd.Recv != nil {
		return false
	}
	if rest := name[len(prefix):]; rest != "" {
		r, _ := utf8.DecodeRuneInString(rest)
		if unicode.IsLower(r) {
			return false
		}
	}
	params := fd.Type.Params
	if params == nil || len(params.List) != 1 || len(params.List[0].Names) > 1 {
		return false
	}
	star, ok := params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	testingName, dot := testingImportName(file)
	switch t := star.X.(type) {
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && testingName != "" && id.Name == testingName && t.Sel.Name == handle
	case *ast.Ident:
		return dot && t.Name == handle
	}
	return false
}

// testingImportName resolves the file-local name of the "testing" import
// and whether it is dot-imported.
func testingImportName(file *ast.File) (string, bool) {
	for _, imp := range file.Imports {
		if imp.Path.Value != `"testing"` {
			continue
		}
		if imp.Name == nil {
			return "testing", false
		}
		if imp.Name.Name == "." {
			return "", true
		}
		return imp.Name.Name, false
	}
	return "", false
}

// moduleModeFlag renders the typed module mode as its -mod flag.
func moduleModeFlag(m stipulatorv1.GoModuleMode) string {
	switch m {
	case stipulatorv1.GoModuleMode_GO_MODULE_MODE_READONLY:
		return "-mod=readonly"
	case stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR:
		return "-mod=vendor"
	case stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD:
		return "-mod=mod"
	}
	return ""
}
