package harden

import (
	"bytes"
	"os"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// fakeBackend drives StagedScope deterministically: canned file surfaces and
// witness classes, so every classification branch — including the
// analyzer-proof-only case the fixture module lacks — is exercised without a
// real Go tree.
type fakeBackend struct {
	surfaces map[string]golang.FileSurface
	classes  map[string]verify.WitnessClass
}

func (f fakeBackend) Surface(paths []string, _ func(string) ([]byte, bool)) []golang.FileSurface {
	out := make([]golang.FileSurface, 0, len(paths))
	for _, p := range paths {
		out = append(out, f.surfaces[p])
	}
	return out
}

// noHead is a HEAD getter that reports every path absent, so every declared
// symbol reads as changed — the whole-file-new baseline.
func noHead(string) ([]byte, bool) { return nil, false }

func (f fakeBackend) WitnessClass(symbol string) verify.WitnessClass {
	return f.classes[symbol]
}

// TestStagedScopeWitnessClass pins the one branch the fixture module cannot
// reach: a bound implements symbol witnessed only by an analyzer proof is
// outside body mutation (witness-class-outside-operators), while the same
// symbol with any example/property witness is covered.
func TestStagedScopeWitnessClass(t *testing.T) {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md": {Data: []byte("# T\n\n" +
			"**REQ-s-proof** (structural): It MUST hold structurally.\n\n" +
			"**REQ-s-tested** (behavior): It MUST behave.\n")},
		".stipulator/bindings/s.textproto": {Data: []byte(`bindings {
  requirement_id: "REQ-s-proof"
  backend: "go"
  symbol: "example.com/x.Proven"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-s-proof"
  backend: "go"
  symbol: "example.com/x.TestProof"
  role: BINDING_ROLE_PROVES
}
bindings {
  requirement_id: "REQ-s-tested"
  backend: "go"
  symbol: "example.com/x.Behaved"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-s-tested"
  backend: "go"
  symbol: "example.com/x.TestBehaved"
  role: BINDING_ROLE_TESTS
}
`)},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	backend := fakeBackend{
		surfaces: map[string]golang.FileSurface{
			"x.go": {Path: "x.go", IsGo: true, Symbols: []string{"example.com/x.Proven", "example.com/x.Behaved"}},
		},
		classes: map[string]verify.WitnessClass{
			"example.com/x.TestProof":   verify.AnalyzerProof,
			"example.com/x.TestBehaved": verify.ExampleWitness,
		},
	}
	rep := StagedScope(spec, store, backend, []string{"x.go"}, noHead)
	got := map[string]SurfaceClass{}
	for _, e := range rep.Entries {
		got[e.Symbol] = e.Class
	}
	if got["example.com/x.Proven"] != WitnessClassOutside {
		t.Errorf("proof-only symbol: class = %q, want witness-class-outside", got["example.com/x.Proven"])
	}
	if got["example.com/x.Behaved"] != Covered {
		t.Errorf("example-witnessed symbol: class = %q, want covered", got["example.com/x.Behaved"])
	}
}

// TestStagedScope pins the staged-delta classification (REQ-harden-staged-
// scope): each changed surface lands in exactly one bucket. Covered =
// bound implements with a body-mutatable witness; a bound symbol without a
// witness is no-witness; a changed function with no implements binding is
// unbound-impl; a generated or non-Go file is generated-or-data; a Go file
// declaring no function is an integration seam.
func TestStagedScope(t *testing.T) {
	spec, store := fixture(t, nil)
	backend, err := golang.New("../backends/golang/testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}

	changed := []string{
		"lib/lib.go",      // Add (covered), Weak (covered), F (no-witness), Mixed+Guarded (unbound)
		"lib/doc.go",      // no funcs -> integration-seam
		"genp/gen.go",     // generated -> generated-or-data
		"lib/data.txt",    // non-Go -> generated-or-data
		"lib/lib_test.go", // a witness source -> excluded entirely
	}
	rep := StagedScope(spec, store, backend, changed, noHead)

	// Test sources are witnesses, not mutation targets: a _test.go file
	// contributes no surface at all.
	for _, e := range rep.Entries {
		if e.Path == "lib/lib_test.go" {
			t.Fatalf("test file surfaced an entry: %+v", e)
		}
	}

	// Symbol-level dispositions.
	wantSym := map[string]SurfaceClass{
		"example.com/fixture/lib.Add":     Covered,
		"example.com/fixture/lib.Weak":    Covered,
		"example.com/fixture/lib.F":       NoWitness,
		"example.com/fixture/lib.Mixed":   UnboundImpl,
		"example.com/fixture/lib.Guarded": UnboundImpl,
	}
	gotSym := map[string]SurfaceClass{}
	fileClass := map[string]SurfaceClass{}
	for _, e := range rep.Entries {
		if e.Symbol != "" {
			gotSym[e.Symbol] = e.Class
			continue
		}
		fileClass[e.Path] = e.Class
	}
	for sym, want := range wantSym {
		if got := gotSym[sym]; got != want {
			t.Errorf("%s: class = %q, want %q", sym, got, want)
		}
	}
	// The type W (a GenDecl, not a func) is never a mutation surface, so it
	// contributes no symbol entry.
	if _, ok := gotSym["example.com/fixture/lib.W"]; ok {
		t.Errorf("type W leaked a symbol entry")
	}

	// File-level buckets.
	if got := fileClass["lib/doc.go"]; got != IntegrationSeam {
		t.Errorf("lib/doc.go: class = %q, want integration-seam", got)
	}
	if got := fileClass["genp/gen.go"]; got != GeneratedOrData {
		t.Errorf("genp/gen.go: class = %q, want generated-or-data", got)
	}
	if got := fileClass["lib/data.txt"]; got != GeneratedOrData {
		t.Errorf("lib/data.txt: class = %q, want generated-or-data", got)
	}

	// Coverable is exactly the covered subset — the surface harden runs.
	cov := rep.Coverable()
	if len(cov) != 2 {
		t.Fatalf("coverable = %d, want 2 (Add, Weak): %+v", len(cov), cov)
	}
}

// TestStagedScopeChangeFilter pins the body-change precision: with a HEAD
// version differing only in Weak's body, only Weak surfaces — Add, F, Mixed,
// and Guarded, whose bodies match HEAD, are dropped. This is what keeps a
// one-function edit from listing every function in the file.
func TestStagedScopeChangeFilter(t *testing.T) {
	spec, store := fixture(t, nil)
	dir := "../backends/golang/testdata/fixturemod"
	backend, err := golang.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	work, err := os.ReadFile(dir + "/lib/lib.go")
	if err != nil {
		t.Fatal(err)
	}
	// The HEAD form differs from the working tree in Weak's body alone.
	headSrc := bytes.Replace(work, []byte("return x - 1"), []byte("return x - 9"), 1)
	if bytes.Equal(headSrc, work) {
		t.Fatal("failed to synthesize a differing HEAD version")
	}
	head := func(p string) ([]byte, bool) {
		if p == "lib/lib.go" {
			return headSrc, true
		}
		return nil, false
	}

	rep := StagedScope(spec, store, backend, []string{"lib/lib.go"}, head)
	var syms []string
	for _, e := range rep.Entries {
		syms = append(syms, e.Symbol)
	}
	if len(syms) != 1 || syms[0] != "example.com/fixture/lib.Weak" {
		t.Fatalf("change filter surfaced %v, want only lib.Weak", syms)
	}

	// A file whose diff lies outside every function body — here only the
	// package doc comment moved — has no changed body, so it is an
	// integration seam, not a per-symbol surface.
	outOfBody := bytes.Replace(work, []byte("hand-written fixture code"), []byte("hand-written fixture code (edited)"), 1)
	if bytes.Equal(outOfBody, work) {
		t.Fatal("failed to synthesize an out-of-body HEAD difference")
	}
	seam := func(p string) ([]byte, bool) {
		if p == "lib/lib.go" {
			return outOfBody, true
		}
		return nil, false
	}
	got := StagedScope(spec, store, backend, []string{"lib/lib.go"}, seam)
	if len(got.Entries) != 1 || got.Entries[0].Class != IntegrationSeam {
		t.Fatalf("out-of-body change: want one integration-seam entry, got %+v", got.Entries)
	}
}
