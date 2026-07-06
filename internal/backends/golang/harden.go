package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"github.com/greatliontech/stipulator/internal/canon"
	"github.com/greatliontech/stipulator/internal/verify"
)

// BodyHash hashes the canonical text of the symbol's body source — the
// function or method body when there is one, the whole declaration
// otherwise. It moves when behavior-bearing code moves and ignores
// formatting churn.
func (b *Backend) BodyHash(symbol string) (string, error) {
	fd, pkg, err := b.funcDecl(symbol)
	if err != nil {
		return "", err
	}
	node := ast.Node(fd)
	if fd.Body != nil {
		node = fd.Body
	}
	src, err := b.sourceOf(pkg, node)
	if err != nil {
		return "", err
	}
	return canon.Hash(canon.Text(string(src))), nil
}

// Vacuous reports whether a test function contains no failure path: no
// failing testing call, no delegation to a callee receiving a testing
// handle, and no panic. Reachability is deliberately not decided here —
// that is what mutation is for.
func (b *Backend) Vacuous(symbol string) (bool, error) {
	fd, pkg, err := b.funcDecl(symbol)
	if err != nil {
		return false, err
	}
	if fd.Body == nil {
		return true, nil
	}
	failing := map[string]bool{"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true}
	vacuous := true
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !vacuous {
			return vacuous
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
			vacuous = false
			return false
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && failing[sel.Sel.Name] {
			vacuous = false
			return false
		}
		for _, arg := range call.Args {
			if carriesTestingHandle(pkg.TypesInfo.TypeOf(arg)) {
				vacuous = false
				return false
			}
		}
		return true
	})
	return vacuous, nil
}

// carriesTestingHandle reports whether t is a testing handle (*testing.T,
// *testing.F, testing.TB) or a function type receiving one — the helper
// and f.Fuzz delegation shapes.
func carriesTestingHandle(t types.Type) bool {
	switch v := t.(type) {
	case nil:
		return false
	case *types.Pointer:
		if n, ok := v.Elem().(*types.Named); ok {
			return isTestingType(n)
		}
	case *types.Named:
		return isTestingType(v)
	case *types.Signature:
		for i := range v.Params().Len() {
			if carriesTestingHandle(v.Params().At(i).Type()) {
				return true
			}
		}
	}
	return false
}

func isTestingType(n *types.Named) bool {
	obj := n.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "testing" &&
		(obj.Name() == "T" || obj.Name() == "F" || obj.Name() == "B" || obj.Name() == "TB")
}

// Mutant is one syntactic mutation of a symbol's body: the full mutated
// file, ready for an overlay run.
type Mutant struct {
	Symbol   string
	Operator string
	Position string
	// BodyLine is the mutated body's first line: positions are absolute,
	// so matching them across regenerations rebases against this anchor.
	BodyLine int
	// File is the original file's absolute path; Source the mutated bytes.
	File   string
	Source []byte
}

// OperatorSet identifies the mutant-generation basis; kill-sheets pin it,
// so extending the operator set re-stales every sheet — an old sheet must
// never claim coverage of mutants it never generated.
const OperatorSet = "go/2"

var comparisonSwap = map[token.Token]token.Token{
	token.EQL: token.NEQ, token.NEQ: token.EQL,
	token.LSS: token.GEQ, token.GEQ: token.LSS,
	token.GTR: token.LEQ, token.LEQ: token.GTR,
	token.LAND: token.LOR, token.LOR: token.LAND,
}

var arithmeticSwap = map[token.Token]token.Token{
	token.ADD: token.SUB, token.SUB: token.ADD,
	token.MUL: token.QUO, token.QUO: token.MUL,
}

var assignArithmeticSwap = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.SUB_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN,
	token.MUL_ASSIGN: token.QUO_ASSIGN, token.QUO_ASSIGN: token.MUL_ASSIGN,
}

// Mutants generates up to budget mutants of the symbol's body (0 means
// all), in source order — deterministic. Mutants that render identically
// to the baseline are dropped here; ones that fail to compile are
// discarded by the runner.
func (b *Backend) Mutants(symbol string, budget int) ([]Mutant, error) {
	fd, pkg, err := b.funcDecl(symbol)
	if err != nil {
		return nil, err
	}
	if fd.Body == nil {
		return nil, nil
	}
	file, path, err := b.fileOf(pkg, fd.Pos())
	if err != nil {
		return nil, err
	}
	baseline, err := renderFile(pkg.Fset, file)
	if err != nil {
		return nil, err
	}

	type site struct {
		op     string
		pos    token.Pos
		apply  func()
		revert func()
	}
	var sites []site

	// numeric reports whether the expression's type is numeric, so
	// arithmetic swaps never touch string concatenation.
	numeric := func(e ast.Expr) bool {
		basic, ok := pkg.TypesInfo.TypeOf(e).(*types.Basic)
		return ok && basic.Info()&types.IsNumeric != 0
	}
	boolTrue, boolFalse := ast.NewIdent("true"), ast.NewIdent("false")

	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BinaryExpr:
			if swapped, ok := comparisonSwap[v.Op]; ok {
				orig := v.Op
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.OpPos,
					apply: func() { v.Op = swapped }, revert: func() { v.Op = orig },
				})
			}
			if swapped, ok := arithmeticSwap[v.Op]; ok && numeric(v.X) {
				orig := v.Op
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.OpPos,
					apply: func() { v.Op = swapped }, revert: func() { v.Op = orig },
				})
			}
			// Forcing one operand of a logical pair to its identity makes
			// the other term decide alone; to its absorbing element, the
			// whole expression — both probe whether the term matters.
			if v.Op == token.LAND || v.Op == token.LOR {
				forced := boolTrue
				if v.Op == token.LOR {
					forced = boolFalse
				}
				for _, side := range []*ast.Expr{&v.X, &v.Y} {
					s, orig := side, *side
					sites = append(sites, site{
						op:    "force " + forced.Name,
						pos:   orig.Pos(),
						apply: func() { *s = forced }, revert: func() { *s = orig },
					})
				}
			}
		case *ast.BasicLit:
			if v.Kind == token.INT {
				orig := v.Value
				sites = append(sites, site{
					op:    "increment literal",
					pos:   v.Pos(),
					apply: func() { v.Value = incrementInt(orig) }, revert: func() { v.Value = orig },
				})
			}
		case *ast.BranchStmt:
			if v.Tok == token.BREAK || v.Tok == token.CONTINUE {
				orig, swapped := v.Tok, token.CONTINUE
				if orig == token.CONTINUE {
					swapped = token.BREAK
				}
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.Pos(),
					apply: func() { v.Tok = swapped }, revert: func() { v.Tok = orig },
				})
			}
		case *ast.IncDecStmt:
			orig, swapped := v.Tok, token.DEC
			if orig == token.DEC {
				swapped = token.INC
			}
			sites = append(sites, site{
				op:    fmt.Sprintf("%s -> %s", orig, swapped),
				pos:   v.TokPos,
				apply: func() { v.Tok = swapped }, revert: func() { v.Tok = orig },
			})
		case *ast.AssignStmt:
			if swapped, ok := assignArithmeticSwap[v.Tok]; ok && numeric(v.Lhs[0]) {
				orig := v.Tok
				sites = append(sites, site{
					op:    fmt.Sprintf("%s -> %s", orig, swapped),
					pos:   v.TokPos,
					apply: func() { v.Tok = swapped }, revert: func() { v.Tok = orig },
				})
			}
		case *ast.IfStmt:
			orig := v.Cond
			sites = append(sites, site{
				op:  "negate condition",
				pos: v.Cond.Pos(),
				apply: func() {
					v.Cond = &ast.UnaryExpr{Op: token.NOT, X: &ast.ParenExpr{X: orig}}
				},
				revert: func() { v.Cond = orig },
			})
		case *ast.BlockStmt:
			for i, st := range v.List {
				switch typed := st.(type) {
				case *ast.ExprStmt, *ast.IncDecStmt, *ast.GoStmt, *ast.DeferStmt, *ast.SendStmt:
					idx, stmt, list := i, st, v
					sites = append(sites, site{
						op:  "delete statement",
						pos: st.Pos(),
						apply: func() {
							list.List = append(append([]ast.Stmt{}, list.List[:idx]...), list.List[idx+1:]...)
						},
						revert: func() {
							withStmt := append(append([]ast.Stmt{}, list.List[:idx]...), stmt)
							list.List = append(withStmt, list.List[idx:]...)
						},
					})
				case *ast.AssignStmt:
					// An assignment cannot be deleted compilably in
					// general, but its store can be dropped: assign the
					// right-hand side to blanks, keeping evaluation and
					// losing the write — the removal-class mutant
					// (leaks, skipped state updates). Declarations stay:
					// removing one breaks later uses, proving nothing.
					if typed.Tok == token.DEFINE {
						break
					}
					blanks := make([]ast.Expr, len(typed.Lhs))
					for j := range blanks {
						blanks[j] = ast.NewIdent("_")
					}
					noop := &ast.AssignStmt{Lhs: blanks, Tok: token.ASSIGN, Rhs: typed.Rhs}
					idx, orig, list := i, st, v
					sites = append(sites, site{
						op:     "drop assignment",
						pos:    st.Pos(),
						apply:  func() { list.List[idx] = noop },
						revert: func() { list.List[idx] = orig },
					})
				}
			}
		case *ast.ReturnStmt:
			for i, res := range v.Results {
				zero := zeroExpr(pkg.TypesInfo.TypeOf(res))
				if zero == nil {
					continue
				}
				idx, orig, ret := i, res, v
				sites = append(sites, site{
					op:    "zero return",
					pos:   res.Pos(),
					apply: func() { ret.Results[idx] = zero }, revert: func() { ret.Results[idx] = orig },
				})
			}
		}
		return true
	})

	var out []Mutant
	seen := map[string]bool{}
	for _, s := range sites {
		if budget > 0 && len(out) >= budget {
			break
		}
		s.apply()
		mutated, err := renderFile(pkg.Fset, file)
		s.revert()
		if err != nil || bytes.Equal(mutated, baseline) {
			continue
		}
		// Two operators occasionally render the same source; running the
		// duplicate would double-count one effective mutant.
		if key := string(mutated); seen[key] {
			continue
		} else {
			seen[key] = true
		}
		// A mutation that orphans an import must not die as a build
		// failure: prune imports so the mutant gets its day in court.
		// Process only formats and prunes here — it must never gain an
		// add-import mode, which would resolve against the inherited
		// (unpinned) workspace.
		if fixed, err := imports.Process("mutant.go", mutated, nil); err == nil {
			mutated = fixed
		}
		p := pkg.Fset.Position(s.pos)
		out = append(out, Mutant{
			Symbol:   symbol,
			Operator: s.op,
			Position: fmt.Sprintf("%s:%d:%d", filepath.Base(p.Filename), p.Line, p.Column),
			BodyLine: pkg.Fset.Position(fd.Pos()).Line,
			File:     path,
			Source:   mutated,
		})
	}
	return out, nil
}

// incrementInt renders an integer literal one greater; non-decimal
// spellings pass through a decimal round-trip only when they parse.
func incrementInt(lit string) string {
	n, err := strconv.ParseUint(lit, 0, 63)
	if err != nil {
		return lit // renders identically and is dropped as a no-op site
	}
	return strconv.FormatUint(n+1, 10)
}

// zeroExpr builds a zero-value expression for simple types; nil when the
// type has no obviously-compilable zero literal.
func zeroExpr(t types.Type) ast.Expr {
	switch v := t.(type) {
	case *types.Basic:
		info := v.Info()
		switch {
		case info&types.IsBoolean != 0:
			return ast.NewIdent("false")
		case info&types.IsNumeric != 0:
			return &ast.BasicLit{Kind: token.INT, Value: "0"}
		case info&types.IsString != 0:
			return &ast.BasicLit{Kind: token.STRING, Value: `""`}
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return ast.NewIdent("nil")
	case *types.Named:
		if _, ok := v.Underlying().(*types.Interface); ok {
			return ast.NewIdent("nil")
		}
	}
	return nil
}

// MutantOutcome classifies one overlay run.
type MutantOutcome int

const (
	// MutantDiscarded: the mutant does not compile (or its run was
	// cancelled); it proves nothing — deliberately the zero value, so an
	// unwritten outcome can never read as a verdict.
	MutantDiscarded MutantOutcome = iota
	// MutantKilled: a bound test failed (or the run timed out — behavior
	// changed).
	MutantKilled
	// MutantSurvived: every bound test passed against the mutant.
	MutantSurvived
)

// SplitRapidPkgs partitions test packages by whether their test files
// (in-package or external variant) import pgregory.net/rapid. Rapid
// packages need -rapid.nofailfile so a mutant-induced property failure
// never writes a reproducer into the source tree — and one mutant's
// failfile cannot replay into the next mutant's run. The flag is
// per-binary: a test binary that does not register it fails on the
// unknown flag and reads as a false kill, so the two groups must run in
// separate invocations. The scan is of direct imports only — a test
// driving rapid solely through a helper package escapes the guard; the
// failure mode there is visible failfile litter, never a false kill.
func (b *Backend) SplitRapidPkgs(testPkgs []string) (rapid, plain []string) {
	byPath := map[string]bool{}
	for _, pkg := range b.pkgs {
		if byPath[pkg.PkgPath] {
			continue
		}
		for _, f := range pkg.Syntax {
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) == rapidPkg {
					byPath[pkg.PkgPath] = true
				}
			}
		}
	}
	for _, p := range testPkgs {
		if byPath[p] || byPath[p+"_test"] {
			rapid = append(rapid, p)
		} else {
			plain = append(plain, p)
		}
	}
	return rapid, plain
}

// TimeoutKiller is the killer attribution of a timed-out mutant run: the
// hang itself is the noticed breakage, so no named test claims the kill.
const TimeoutKiller = "(timeout)"

// PackageKillerPrefix prefixes the killer attribution of a mutant that
// breaks a test binary at package scope — a panic in a goroutine, an
// os.Exit, a TestMain failure — where go test emits no test-level fail
// event. Such a kill is admitted only after a differential baseline probe
// clears the environment.
const PackageKillerPrefix = "(package failure: "

// RunMutant executes the bound tests against one mutant through a build
// overlay — the tree is never touched. binFlags are passed to the test
// binaries after the package list.
//
// A kill must be attributed: a named failing test in the run's -json
// stream (returned as "<pkg>.<TopLevelTest>"), a timeout (TimeoutKiller —
// behavior changed: it hangs), or a package-scope failure the baseline
// probe attributes to the mutant (PackageKillerPrefix). A run that fails
// any other way is environmental noise — an unregistered flag, a loaded
// machine, a dying binary — and returns an error, never a kill: noise
// once inflated kill counts in the flattering direction, and a corrupted
// measurement must never read as a sound one.
func RunMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string) (MutantOutcome, string, error) {
	tmp, err := os.MkdirTemp("", "stipulator-mutant-*")
	if err != nil {
		return MutantDiscarded, "", err
	}
	defer os.RemoveAll(tmp)
	mutFile := filepath.Join(tmp, "mutant.go")
	if err := os.WriteFile(mutFile, m.Source, 0o644); err != nil {
		return MutantDiscarded, "", err
	}
	overlay := filepath.Join(tmp, "overlay.json")
	oj := fmt.Sprintf(`{"Replace": {%q: %q}}`, m.File, mutFile)
	if err := os.WriteFile(overlay, []byte(oj), 0o644); err != nil {
		return MutantDiscarded, "", err
	}

	parent := ctx
	runCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// -failfast: one witness failure decides the binary's verdict; the
	// remaining tests in it prove nothing further about this mutant.
	baseArgs := append([]string{"test", "-json", "-count=1", "-failfast", "-run", runRegex}, testPkgs...)
	baseArgs = append(baseArgs, binFlags...)
	args := append([]string{"test", "-json", "-overlay", overlay, "-count=1", "-failfast", "-run", runRegex}, testPkgs...)
	args = append(args, binFlags...)
	cmd := exec.CommandContext(runCtx, "go", args...)
	cmd.Dir = dir
	cmd.Env = goworkEnv(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	killer := firstFailingTest(stdout.Bytes())
	switch {
	case runErr == nil:
		return MutantSurvived, "", nil
	case runCtx.Err() == context.DeadlineExceeded:
		return MutantKilled, TimeoutKiller, nil
	case runCtx.Err() != nil:
		// A cancelled run proves nothing about the mutant.
		return MutantDiscarded, "", ctx.Err()
	case strings.Contains(stdout.String(), "[build failed]"):
		return MutantDiscarded, "", nil
	case killer != "":
		return MutantKilled, killer, nil
	}

	// The run failed with no test-level attribution. Two very different
	// causes share this shape: the mutant breaking the binary at package
	// scope (a goroutine panic, an os.Exit, a TestMain failure — the
	// strongest kind of kill), and environmental noise (the shape that
	// once inflated five sheets). A differential baseline probe — the
	// same invocation without the overlay — tells them apart: noise
	// fails the baseline too; a mutant-caused break does not.
	if pkg := failedPackage(stdout.Bytes()); pkg != "" {
		baseCtx, baseCancel := context.WithTimeout(parent, timeout)
		defer baseCancel()
		base := exec.CommandContext(baseCtx, "go", baseArgs...)
		base.Dir = dir
		base.Env = goworkEnv(dir)
		baseErr := base.Run()
		if baseCtx.Err() != nil {
			// A cancelled probe proves nothing — never "noise".
			return MutantDiscarded, "", baseCtx.Err()
		}
		if baseErr == nil {
			return MutantKilled, PackageKillerPrefix + pkg + ")", nil
		}
	}
	return MutantDiscarded, "", fmt.Errorf("mutant run failed with no test-attributed kill (environmental noise, not a kill; baseline probe did not clear it): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
}

// failedPackage scans a go test -json stream for a package-level fail
// event, returning the package or empty.
func failedPackage(stream []byte) string {
	type event struct {
		Action, Package, Test string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			return ""
		}
		if e.Action == "fail" && e.Test == "" && e.Package != "" {
			return e.Package
		}
	}
	return ""
}

// firstFailingTest scans a go test -json stream for the first test-level
// fail event, returning the failing test as "<pkg>.<TopLevelTest>" — the
// symbol form witness sets pin. The subtest path is stripped HERE, where
// the Test field is unambiguous; in the joined form the first "/" lands
// inside the import path.
func firstFailingTest(stream []byte) string {
	type event struct {
		Action, Package, Test string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			return ""
		}
		if e.Action == "fail" && e.Test != "" {
			name := e.Test
			if i := strings.Index(name, "/"); i >= 0 {
				name = name[:i]
			}
			return e.Package + "." + name
		}
	}
	return ""
}

// tail returns the last n bytes of s, for error surfacing.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// ErrNotFunction marks a resolvable symbol with no function body — a
// type or variable. Body-level operations skip such symbols; there is
// nothing to hash or mutate.
var ErrNotFunction = errors.New("is not a function or method")

// funcDecl resolves a symbol to its declaring FuncDecl and package.
func (b *Backend) funcDecl(symbol string) (*ast.FuncDecl, *packages.Package, error) {
	res, _, err := b.Resolve(symbol)
	if err != nil {
		return nil, nil, err
	}
	if res != verify.Resolved {
		return nil, nil, fmt.Errorf("symbol %s does not resolve", symbol)
	}
	obj := b.object(symbol)
	if obj == nil {
		return nil, nil, fmt.Errorf("symbol %s has no object", symbol)
	}
	for _, pkg := range b.pkgs {
		for _, f := range pkg.Syntax {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if pkg.TypesInfo.Defs[fd.Name] == obj {
					return fd, pkg, nil
				}
			}
		}
	}
	return nil, nil, fmt.Errorf("symbol %s: %w", symbol, ErrNotFunction)
}

// fileOf finds the syntax file (and its absolute path) containing pos.
func (b *Backend) fileOf(pkg *packages.Package, pos token.Pos) (*ast.File, string, error) {
	for _, f := range pkg.Syntax {
		if f.FileStart <= pos && pos < f.FileEnd {
			return f, pkg.Fset.Position(f.Pos()).Filename, nil
		}
	}
	return nil, "", fmt.Errorf("no syntax file for position")
}

// sourceOf reads the original source bytes spanned by node.
func (b *Backend) sourceOf(pkg *packages.Package, node ast.Node) ([]byte, error) {
	start := pkg.Fset.Position(node.Pos())
	end := pkg.Fset.Position(node.End())
	data, err := os.ReadFile(start.Filename)
	if err != nil {
		return nil, err
	}
	if start.Offset < 0 || end.Offset > len(data) || start.Offset > end.Offset {
		return nil, fmt.Errorf("node span out of range in %s", start.Filename)
	}
	return data[start.Offset:end.Offset], nil
}

func renderFile(fset *token.FileSet, file *ast.File) ([]byte, error) {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
