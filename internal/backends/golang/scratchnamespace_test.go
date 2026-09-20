package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestScratchNamespacesAreAcceptedAsDeclared pins the policy record's
// scratch namespace rows: gofresh's grammar refuses a malformed row, a
// repeated row is refused, an exclusion naming a surface a namespace
// covers is refused — the namespace retires it — and an accepted set
// reaches the normalized invocation canonical (REQ-evidence-witness-freshness).
func TestScratchNamespacesAreAcceptedAsDeclared(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	ns := func(dir, pattern string) *stipulatorv1.ScratchNamespace {
		n := &stipulatorv1.ScratchNamespace{}
		n.SetDir(dir)
		n.SetPattern(pattern)
		return n
	}
	cfg := func(rows ...*stipulatorv1.ScratchNamespace) *stipulatorv1.GoInvocationConfig {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./..."})
		c.SetScratchNamespaces(rows)
		return c
	}
	if err := validateConfig(cfg(ns("scratch", "run-*/x"))); err == nil {
		t.Fatal("a multi-component pattern was accepted")
	}
	if err := validateConfig(cfg(ns("../out", "run-*"))); err == nil {
		t.Fatal("a directory escaping the module was accepted")
	}
	if err := validateConfig(cfg(ns("scratch", "run-*"), ns("scratch", "run-*"))); err == nil {
		t.Fatal("a repeated namespace row was accepted")
	}
	if err := validateConfig(cfg(ns("./scratch", "run-*"))); err == nil {
		t.Fatal("a directory not in clean slash form was accepted; canonical form is refused, never repaired")
	}
	if err := validateConfig(cfg(ns(".git/tmp", "run-*"))); err == nil {
		t.Fatal("a namespace inside the VCS tree was accepted; the ingest excludes it")
	}
	// One declaration per surface: an exclusion covering the directory
	// (equal, or above it) leaves the namespace admitting nothing; an
	// exclusion on or beneath a child the pattern names is the interim
	// the namespace retires; a sibling the pattern does not name is
	// neither and stays excludable.
	for _, excluded := range []string{"scratch", "scr", "scratch/run-1", "scratch/run-1/note", "scratch/run-"} {
		covered := cfg(ns("scratch", "run-*"))
		covered.SetExcludedPaths([]string{excluded})
		if excluded == "scr" {
			if err := validateConfig(covered); err != nil {
				t.Fatalf("a prefix-only sibling directory %q refused: %v", excluded, err)
			}
			continue
		}
		if err := validateConfig(covered); err == nil {
			t.Fatalf("exclusion %q beside namespace scratch/run-* was accepted", excluded)
		}
	}
	sibling := cfg(ns("scratch", "run-*"))
	sibling.SetExcludedPaths([]string{"scratch/keep", "scratch/xrun-1"})
	if err := validateConfig(sibling); err != nil {
		t.Fatalf("siblings the pattern does not name refused: %v", err)
	}
	if !namespaceCoversPath("scratch", "run-*.tmp", "scratch/run-9.tmp/x") || namespaceCoversPath("scratch", "run-*.tmp", "scratch/run-9.log") || !namespaceCoversPath(".", "keep", "keep/x") {
		t.Fatal("the pattern's prefix and suffix halves are not gofresh's rule")
	}
	rows := cfg(ns("z", "b-*"), ns("a", "run-*"), ns("z", "a-*"), ns("a", "run-*"))
	if err := validateConfig(rows); err == nil {
		t.Fatal("a repeated row among others was accepted")
	}
	rows.SetScratchNamespaces([]*stipulatorv1.ScratchNamespace{ns("z", "b-*"), ns("a", "run-*"), ns("z", "a-*")})
	if err := validateConfig(rows); err != nil {
		t.Fatalf("a well-formed set refused: %v", err)
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/scratch\n\ngo 1.26\n", "p.go": "package scratch\n"})
	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("x", rows))
	if err != nil {
		t.Fatal(err)
	}
	want := []runtimeinput.ScratchNamespace{{Dir: "a", Pattern: "run-*"}, {Dir: "z", Pattern: "a-*"}, {Dir: "z", Pattern: "b-*"}}
	if len(n.ScratchNamespaces) != len(want) {
		t.Fatalf("normalized namespaces = %+v, want %+v", n.ScratchNamespaces, want)
	}
	for i := range want {
		if n.ScratchNamespaces[i] != want[i] {
			t.Fatalf("normalized namespaces = %+v, want the canonical order %+v", n.ScratchNamespaces, want)
		}
	}
	if got := namespaceRows(n.ScratchNamespaces); len(got) != 3 || got[0] != string(quotedList([]string{"a", "run-*"})) {
		t.Fatalf("key rows = %q", got)
	}
	// The pair is quoted structurally: no directory or pattern byte
	// aliases one pair into another.
	if a, b := namespaceRows([]runtimeinput.ScratchNamespace{{Dir: "a\x00b", Pattern: "c"}}), namespaceRows([]runtimeinput.ScratchNamespace{{Dir: "a", Pattern: "b\x00c"}}); a[0] == b[0] {
		t.Fatalf("two pairs rendered as one row %q", a[0])
	}
}

// TestGoRunWitnessesServeUnderADeclaredScratchNamespace pins the
// declaration end to end. A witness minting, reading, and removing its
// own scratch records, without a declaration, the scratch path as an
// absent input — an absence probe, so a file appearing there later
// stales the record and re-executes the witness; under the declared
// namespace the read enters no path identity at all and the record
// serves past its own scratch (REQ-evidence-witness-freshness;
// gofresh's scratch-namespace contract).
func TestGoRunWitnessesServeUnderADeclaredScratchNamespace(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("runs a race-instrumented witness pass over a temporary module, twice per arm")
	}
	files := map[string]string{
		"go.mod":       "module example.com/scratchfixture\n\ngo 1.26\n",
		"scratch/keep": "",
		"lib.go":       "package scratchfixture\n\nfunc Two() int { return 2 }\n",
		"lib_test.go": `package scratchfixture

import (
	"os"
	"path/filepath"
	"testing"
)

// The test's own scratch is asserted pure — the discipline the
// namespace declaration rides beside: the observation proof is the
// assertion's, and the runtime-input manifest is where the declaration
// decides.
//
//gofresh:pure
func TestUsesScratch(t *testing.T) {
	dir, err := os.MkdirTemp("scratch", "run-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "note"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "note")); err != nil || string(b) != "two" {
		t.Fatalf("scratch read = %q, %v", b, err)
	}
	if Two() != 2 {
		t.Fatal("wrong")
	}
}
`,
	}
	for name, declared := range map[string]bool{"declared": true, "undeclared": false} {
		t.Run(name, func(t *testing.T) {
			neutralAmbient(t)
			tmp := writeModule(t, files)
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{"./..."})
			cfg.SetRace(true)
			if declared {
				ns := &stipulatorv1.ScratchNamespace{}
				ns.SetDir("scratch")
				ns.SetPattern("run-*")
				cfg.SetScratchNamespaces([]*stipulatorv1.ScratchNamespace{ns})
			}
			p := &stipulatorv1.TestPolicy{}
			p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
			writePolicyRecord(t, tmp, p)
			first, err := RunWitnesses(context.Background(), tmp, noSeeding{})
			if err != nil {
				t.Fatal(err)
			}
			if first.Ran != 1 || first.Fresh != 0 || first.Uncached != 0 {
				t.Fatalf("first run: ran=%d fresh=%d uncached=%d (%v), want 1 ran and recorded", first.Ran, first.Fresh, first.Uncached, first.UncacheableReasons)
			}
			// The record's manifest: the scratch read's path identity is
			// there without the declaration and absent under it.
			records := witnesscache.Load(tmp)
			if len(records) != 1 {
				t.Fatalf("records after the first run = %d, want 1", len(records))
			}
			desc, err := runtimeinput.Describe(records[0].Fingerprint.RuntimeInputs, tmp)
			if err != nil {
				t.Fatal(err)
			}
			var scratchPaths []string
			for _, p := range desc.Paths {
				if rel, err := filepath.Rel(tmp, p); err == nil && strings.HasPrefix(rel, "scratch"+string(filepath.Separator)+"run-") {
					scratchPaths = append(scratchPaths, p)
				}
			}
			if declared && len(scratchPaths) != 0 {
				t.Fatalf("declared: the manifest names the scratch read %q; the namespace must admit it without an identity", scratchPaths)
			}
			if !declared && len(scratchPaths) == 0 {
				t.Fatalf("undeclared: the manifest names no scratch path among %q; the fixture's read must be an absent-probe entry", desc.Paths)
			}
			// A file appearing where the run's scratch was: an absent
			// probe's appearance stales the undeclared record. The
			// declared one holds no such path, so nothing appears for
			// it — an appearance anywhere under the namespace directory
			// would move that directory's own listing, an identity the
			// namespace does not cover (it names the matching children
			// and what lies beneath them, never their parent).
			if !declared {
				planted := scratchPaths[0]
				if err := os.MkdirAll(filepath.Dir(planted), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(planted, []byte("appeared"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			second, err := RunWitnesses(context.Background(), tmp, noSeeding{})
			if err != nil {
				t.Fatal(err)
			}
			if declared && (second.Fresh != 1 || second.Ran != 0) {
				t.Fatalf("declared: second run ran=%d fresh=%d uncached=%d (%v %v), want the record served past its own scratch", second.Ran, second.Fresh, second.Uncached, second.ExecutedReasons, second.UncacheableReasons)
			}
			if declared {
				// The declaration withdrawn: the record elided reads under
				// a licence the policy no longer grants, so it re-executes
				// with that reason.
				cfg.SetScratchNamespaces(nil)
				writePolicyRecord(t, tmp, p)
				third, err := RunWitnesses(context.Background(), tmp, noSeeding{})
				if err != nil {
					t.Fatal(err)
				}
				if third.Ran != 1 || third.Fresh != 0 || third.ExecutedReasons["example.com/scratchfixture.TestUsesScratch"] != refusedWithdrawnNamespace {
					t.Fatalf("withdrawn: third run ran=%d fresh=%d reasons=%v, want re-execution under the withdrawn-namespace reason", third.Ran, third.Fresh, third.ExecutedReasons)
				}
			}
			if !declared && (second.Ran != 1 || second.Fresh != 0) {
				t.Fatalf("undeclared: second run ran=%d fresh=%d uncached=%d, want the absent probe's appearance to re-execute", second.Ran, second.Fresh, second.Uncached)
			}
		})
	}
}

// TestScratchNamespaceCoveredByNoBracketRootIsRefused pins discovery's
// refusal of a declaration no observation-bracket root of the
// invocation covers: the engine would admit nothing under it, and an
// inert declaration never sits in the reviewed record silently
// (REQ-evidence-witness-freshness).
func TestScratchNamespaceCoveredByNoBracketRootIsRefused(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("lists a temporary module's packages")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/uncovered\n\ngo 1.26\n",
		"elsewhere/keep":  "",
		"lib/lib.go":      "package lib\n\nfunc Two() int { return 2 }\n",
		"lib/lib_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestTwo(t *testing.T) {\n\tif Two() != 2 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./lib/..."})
	ns := &stipulatorv1.ScratchNamespace{}
	ns.SetDir("elsewhere")
	ns.SetPattern("run-*")
	cfg.SetScratchNamespaces([]*stipulatorv1.ScratchNamespace{ns})
	n, err := NormalizeInvocation(context.Background(), tmp, goInvocation("x", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(context.Background(), n); err == nil || !strings.Contains(err.Error(), "covered by no package directory") {
		t.Fatalf("an uncovered namespace was accepted at discovery: %v", err)
	}
	// An absolute bracket path, even inside the tree, covers no
	// namespace: the engine grants scratch admission under its relative
	// roots alone.
	cfg.SetBracketPaths([]string{filepath.Join(tmp, "elsewhere")})
	n, err = NormalizeInvocation(context.Background(), tmp, goInvocation("x", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(context.Background(), n); err == nil || !strings.Contains(err.Error(), "covered by no package directory") {
		t.Fatalf("an absolute bracket path covered the namespace: %v", err)
	}
	cfg.SetBracketPaths(nil)
	ns.SetDir("lib/scratch")
	n, err = NormalizeInvocation(context.Background(), tmp, goInvocation("x", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(context.Background(), n); err != nil {
		t.Fatalf("a namespace under the selected package's directory refused: %v", err)
	}
	// The tree named through a symbolic link: the listing's physical
	// directories still relativize under the frame's resolved base, so
	// the covered namespace is accepted there too.
	link := filepath.Join(t.TempDir(), "via")
	if err := os.Symlink(tmp, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	linked, err := NormalizeInvocation(context.Background(), link, goInvocation("x", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(context.Background(), linked); err != nil {
		t.Fatalf("a covered namespace refused under a tree named through a link: %v", err)
	}
	// The directory half of the frame's rule: a package reached through
	// an in-tree link is declared to the engine under its resolved
	// spelling (the frame composes every identity from the declared
	// root), so a namespace under the resolved spelling is covered and
	// one under the link's spelling is not.
	linkedTree := writeModule(t, map[string]string{
		"go.mod":               "module example.com/linked\n\ngo 1.26\n",
		"real/lib/lib.go":      "package lib\n\nfunc Two() int { return 2 }\n",
		"real/lib/lib_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestTwo(t *testing.T) {\n\tif Two() != 2 {\n\t\tt.Fatal(\"wrong\")\n\t}\n}\n",
	})
	if err := os.Symlink(filepath.Join(linkedTree, "real", "lib"), filepath.Join(linkedTree, "lib")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	for spelling, accepted := range map[string]bool{"real/lib/scratch": true, "lib/scratch": false} {
		c := &stipulatorv1.GoInvocationConfig{}
		c.SetPackages([]string{"./lib/..."})
		row := &stipulatorv1.ScratchNamespace{}
		row.SetDir(spelling)
		row.SetPattern("run-*")
		c.SetScratchNamespaces([]*stipulatorv1.ScratchNamespace{row})
		m, err := NormalizeInvocation(context.Background(), linkedTree, goInvocation("x", c))
		if err != nil {
			t.Fatal(err)
		}
		_, err = DiscoverInvocation(context.Background(), m)
		if accepted && err != nil {
			t.Fatalf("the resolved spelling %q refused under an in-tree link: %v", spelling, err)
		}
		if !accepted && (err == nil || !strings.Contains(err.Error(), "covered by no package directory")) {
			t.Fatalf("the link's spelling %q accepted; the engine declares the resolved root: %v", spelling, err)
		}
	}
	// The precondition, pure over the invocation: an uncovered namespace
	// refuses only over a listed closure — a run whose closure could
	// not be listed admits nothing anywhere and is not refused here.
	bare := &NormalizedInvocation{Name: "x", Dir: tmp, ScratchNamespaces: []runtimeinput.ScratchNamespace{{Dir: "elsewhere", Pattern: "run-*"}}}
	if err := scratchNamespacesCovered(bare); err == nil {
		t.Fatal("an uncovered namespace over an empty listed closure was accepted")
	}
	bare.ClosureDirsErr = "listing failed"
	if err := scratchNamespacesCovered(bare); err != nil {
		t.Fatalf("a namespace over an unlisted closure was judged: %v", err)
	}
}
