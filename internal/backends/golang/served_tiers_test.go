package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestClosureMovedNamesEveryTier pins the equivalence's four tiers, one
// anchor each, in gofresh's order: a move in any one of the maximal
// closure, the test-variant compartment, the toolchain, or the build
// configuration refuses the serve by name; equal tiers serve; an empty
// recorded compartment fails closed.
func TestClosureMovedNamesEveryTier(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	base := func() (witnesscache.Fingerprint, gofresh.Fingerprint) {
		rec := witnesscache.Fingerprint{MaximalClosure: strings.Repeat("a", 32), TestVariantClosure: strings.Repeat("b", 32), Toolchain: "go1.27.0", BuildConfig: strings.Repeat("c", 32)}
		cur := gofresh.Fingerprint{MaximalClosure: rec.MaximalClosure, TestVariantClosure: rec.TestVariantClosure}
		cur.Guards.Toolchain, cur.Guards.BuildConfig = rec.Toolchain, rec.BuildConfig
		return rec, cur
	}
	if rec, cur := base(); closureMoved(rec, cur) != "" {
		t.Fatalf("equal tiers refused: %q", closureMoved(rec, cur))
	}
	for _, tc := range []struct {
		name string
		move func(*witnesscache.Fingerprint)
	}{
		{"closure", func(f *witnesscache.Fingerprint) { f.MaximalClosure = strings.Repeat("d", 32) }},
		{"test variants", func(f *witnesscache.Fingerprint) { f.TestVariantClosure = strings.Repeat("d", 32) }},
		{"test variants", func(f *witnesscache.Fingerprint) { f.TestVariantClosure = "" }},
		{"toolchain", func(f *witnesscache.Fingerprint) { f.Toolchain = "go1.26.0" }},
		{"build configuration", func(f *witnesscache.Fingerprint) { f.BuildConfig = strings.Repeat("d", 32) }},
	} {
		rec, cur := base()
		tc.move(&rec)
		if got := closureMoved(rec, cur); got != tc.name {
			t.Fatalf("%s moved: refused as %q", tc.name, got)
		}
	}
}

// TestServedIgnoresRecordsOfWithdrawnSelections pins the selection key:
// a record under a selection the current policy does not declare is
// never checked against another view's closure — it does not serve.
func TestServedIgnoresRecordsOfWithdrawnSelections(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	first, err := NewServed(ctx, dir, servedSymbols[:2])
	if err != nil {
		t.Fatal(err)
	}
	ask(t, first, "example.com/served/p.F")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	var fp witnesscache.Fingerprint
	for _, rec := range resolutioncache.Load(dir) {
		if rec.Symbol == "example.com/served/p.F" {
			fp = rec.Fingerprint
			if rec.Selection != "default" {
				t.Fatalf("F recorded under %q, want the default selection", rec.Selection)
			}
		}
	}
	// The same fingerprint under a selection the policy never declared.
	withdrawn := resolutioncache.Record{Selection: "withdrawn\x00go1.0", Symbol: "example.com/served/p.H", Fingerprint: fp, Resolution: "resolved", Shape: "bogus", Package: "example.com/served/p"}
	if err := resolutioncache.Install(dir, withdrawn); err != nil {
		t.Fatal(err)
	}
	c := countSpawns(t)
	second, err := NewServed(ctx, dir, servedSymbols[:2])
	if err != nil {
		t.Fatal(err)
	}
	if got := ask(t, second, "example.com/served/p.H"); got.shape == "bogus" || c.snapshot().child != 1 {
		t.Fatalf("H under a withdrawn selection: served %+v with %d children; want typed", got, c.snapshot().child)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestServedReresolvesWhenATestBodyChangesClass pins the compartment
// tier from the fault's own direction: a test whose body gains the
// random-seeded driver call — a test-only edit that moves no core
// closure — re-resolves, and its record then carries the serving
// refusal a random-seeded witness must carry.
func TestServedReresolvesWhenATestBodyChangesClass(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness", "REQ-go-witness-class")
	if testing.Short() {
		t.Skip("loads a fixture module's views")
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":                       "module example.com/seeded\n\ngo 1.26\n\nrequire pgregory.net/rapid v1.3.0\n",
		"go.sum":                       "pgregory.net/rapid v1.3.0 h1:vBvO0VSqti75J1jjYqpgPNBLKMd1+gxa9fYo7vk/Exc=\npgregory.net/rapid v1.3.0/go.mod h1:dPlE4OBBxgXPqkP79flB6sJL1dx5azpI7HQ9MY9Z7uk=\n",
		"lib/lib.go":                   "package lib\n\nfunc Add(a, b int) int { return a + b }\n",
		"lib/a_test.go":                "package lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc drive(t *testing.T) {\n\trapid.Check(t, func(rt *rapid.T) {\n\t\tif Add(2, 2) != 4 {\n\t\t\tpanic(\"broken\")\n\t\t}\n\t})\n}\n",
		"lib/b_test.go":                "package lib\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tdrive(t)\n}\n",
		".stipulator/policy.textproto": "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
	})
	ctx := context.Background()
	const symbol = "example.com/seeded/lib.TestB"
	first, err := NewServed(ctx, dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	// The helper-driven form keeps its example class and is already
	// refused serving through the helper (the transitive seeding class);
	// the inlined form below re-resolves to the direct refusal, so the
	// record still moves with the compartment.
	if got := ask(t, first, symbol); got.class != verify.ExampleWitness || got.refusal != seededThroughReason("example.com/seeded/lib.drive") {
		t.Fatalf("a helper-driven test classed %+v; want example, refused through the helper", got)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Inline the driver call into TestB's own body: the core closure is
	// untouched, only the package's test-variant compartment moves.
	if err := os.WriteFile(filepath.Join(dir, "lib", "b_test.go"), []byte("package lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc TestB(t *testing.T) {\n\trapid.Check(t, func(rt *rapid.T) {\n\t\tif Add(2, 2) != 4 {\n\t\t\tpanic(\"broken\")\n\t\t}\n\t})\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := countSpawns(t)
	second, err := NewServed(ctx, dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	got := ask(t, second, symbol)
	if got.class != verify.PropertyWitness || got.refusal != seededReason || c.snapshot().child != 1 {
		t.Fatalf("after the body gained the driver call: %+v with %d children (reasons %v); want property, the seeded refusal, one typed resolution", got, c.snapshot().child, second.Reasons())
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	for _, rec := range resolutioncache.Load(dir) {
		if rec.Symbol == symbol && (rec.WitnessClass != "property" || rec.NeverServe != seededReason) {
			t.Fatalf("republished record %+v carries no seeded refusal", rec)
		}
	}
	// The class now depends on the helper's body too: restore the
	// helper-driven TestB and drop the driver from the helper — the
	// record re-resolves to a served example witness with no refusal.
	if err := os.WriteFile(filepath.Join(dir, "lib", "b_test.go"), []byte("package lib\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tdrive(t)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "a_test.go"), []byte("package lib\n\nimport \"testing\"\n\nfunc drive(t *testing.T) {\n\tif Add(1, 1) != 2 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := NewServed(ctx, dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	if got := ask(t, third, symbol); got.class != verify.ExampleWitness || got.refusal != "" {
		t.Fatalf("after the helper lost the driver: %+v; want example, no refusal", got)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestServedDegradesASelectionToTyped pins REQ-evidence-freshness-degrade
// on the serving path: a selection whose view cannot be built — here a
// package that no longer parses — degrades every recorded symbol of the
// selection to the typed resolution, names the fault, and the run still
// answers.
func TestServedDegradesASelectionToTyped(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade", "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	first, err := NewServed(ctx, dir, servedSymbols[:2])
	if err != nil {
		t.Fatal(err)
	}
	ask(t, first, "example.com/served/p.F")
	ask(t, first, "example.com/served/p.H")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "broken.go"), []byte("package p\n\nfunc broken( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := countSpawns(t)
	second, err := NewServed(ctx, dir, servedSymbols[:2])
	if err != nil {
		t.Fatalf("a broken view failed the operation instead of degrading: %v", err)
	}
	if second.ServedCount() != 0 || len(second.Degraded()) == 0 {
		t.Fatalf("served %d, degraded %v; want the selection degraded whole", second.ServedCount(), second.Degraded())
	}
	// The typed path answers the broken package as the whole tree does:
	// its load error, loudly, through exactly one child.
	if res, _, _, err := second.ResolveIn("example.com/served/p.F"); c.snapshot().child != 1 || err == nil || !strings.Contains(err.Error(), "load errors") {
		t.Fatalf("degraded symbol resolved %v, %v with %d children; want the typed path's load error", res, err, c.snapshot().child)
	}
	notices := strings.Join(second.Notices(), "\n")
	if !strings.Contains(notices, "resolution degraded to typed: selection") {
		t.Fatalf("notices %q name no degradation", notices)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestScopedChildAnswersLikeTheWholeTree pins the scoped typed load: a
// child loading only the stale symbols' packages answers every field
// as the whole-tree child does — including the generated-file verdict
// of a method promoted from a type declared in a generated file of a
// dependency package, which the scoped load carries as a dependency.
func TestScopedChildAnswersLikeTheWholeTree(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness", "REQ-evidence-generated-code")
	if testing.Short() {
		t.Skip("loads a fixture module's types twice")
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.com/scoped\n\ngo 1.26\n",
		"r/gen.go": "// Code generated by tool. DO NOT EDIT.\n\npackage r\n\ntype Base struct{}\n\nfunc (Base) Gen() int { return 1 }\n",
		"q/q.go":   "package q\n\nimport \"example.com/scoped/r\"\n\ntype T struct{ r.Base }\n\nfunc F() int { return T{}.Gen() }\n",
		"s/s.go":   "package s\n\nfunc Unrelated() {}\n",
		"x/x.go":   "//go:build never\n\npackage x\n\nfunc X() {}\n",
		"bad/b.go": "package bad\n\nfunc B() int { return \"not an int\" }\n",
	})
	ctx := context.Background()
	// The vanished package is the everyday stale binding: its pattern
	// matches nothing, and the scoped load must answer not found for
	// its symbols as "./..." does, never a load error; a package whose
	// every file the selection excludes is the same case — "./..."
	// never enumerates it, while its explicit pattern lists an error
	// entry without Go files.
	// A package that lists but fails to type-check keeps its load error
	// under both loads: only a pattern matching nothing is dropped.
	symbols := []string{"example.com/scoped/q.F", "example.com/scoped/q.T.Gen", "example.com/scoped/q.T", "example.com/scoped/gone.X", "example.com/scoped/x.X", "example.com/scoped/bad.B"}
	whole, err := NewOwned(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	scoped, err := NewOwnedScoped(ctx, dir, []string{"example.com/scoped/q", "example.com/scoped/gone", "example.com/scoped/x", "example.com/scoped/bad"})
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()
	for _, symbol := range symbols {
		wr, ws, wsel, werr := whole.ResolveIn(symbol)
		sr, ss, ssel, serr := scoped.ResolveIn(symbol)
		if wr != sr || ws != ss || wsel != ssel || (werr == nil) != (serr == nil) {
			t.Fatalf("%s: whole %v %q %q %v; scoped %v %q %q %v", symbol, wr, ws, wsel, werr, sr, ss, ssel, serr)
		}
		if (symbol == "example.com/scoped/gone.X" || symbol == "example.com/scoped/x.X") && (serr != nil || sr != verify.NotFound) {
			t.Fatalf("%s: unheld package under the scoped load: %v, %v; want not found", symbol, sr, serr)
		}
		if symbol == "example.com/scoped/bad.B" && (serr == nil || !strings.Contains(serr.Error(), "load errors")) {
			t.Fatalf("%s: broken package under the scoped load: %v, %v; want its load error", symbol, sr, serr)
		}
		wp, _ := whole.SymbolPackage(symbol)
		sp, _ := scoped.SymbolPackage(symbol)
		if wp != sp {
			t.Fatalf("%s: package whole %q scoped %q", symbol, wp, sp)
		}
	}
	if res, _, _, _ := scoped.ResolveIn("example.com/scoped/q.T.Gen"); res != verify.GeneratedFile {
		t.Fatalf("the promoted generated method resolved %v under the scoped load; want the generated-file verdict", res)
	}
	// A package outside the scope is unknown to the scoped child, which
	// answers not found for it — the served backend never asks it about
	// one (TestServedRefusesSymbolsOutsideTheOperation).
	if res, _, _, err := scoped.ResolveIn("example.com/scoped/s.Unrelated"); err != nil || res != verify.NotFound {
		t.Fatalf("out-of-scope symbol: %v, %v; want not found", res, err)
	}
}

// TestServedRefusesSymbolsOutsideTheOperation pins the served backend's
// boundary (REQ-evidence-resolution-freshness): a symbol the operation
// never named is refused on every role — resolution, package, class,
// serving refusal — rather than forwarded to the scoped child, whose
// narrowed frontier would answer not found for what the tree declares.
func TestServedRefusesSymbolsOutsideTheOperation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's types")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":      "module example.com/scoped\n\ngo 1.26\n",
		"q/q.go":      "package q\n\nfunc F() int { return 1 }\n",
		"s/s.go":      "package s\n\nfunc Unrelated() {}\n",
		"s/s_test.go": "package s\n\nimport \"testing\"\n\nfunc TestS(t *testing.T) {}\n",
	})
	served, err := NewServed(context.Background(), dir, []string{"example.com/scoped/q.F"})
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	if res, _, _, err := served.ResolveIn("example.com/scoped/q.F"); err != nil || res != verify.Resolved {
		t.Fatalf("named symbol: %v, %v; want resolved", res, err)
	}
	outside := "example.com/scoped/s.Unrelated"
	if _, _, _, err := served.ResolveIn(outside); err == nil || !strings.Contains(err.Error(), "outside this operation's symbol set") {
		t.Fatalf("out-of-set resolution: %v; want the refusal", err)
	}
	if _, err := served.SymbolPackage(outside); err == nil {
		t.Fatal("out-of-set package answered")
	}
	if _, reason := served.WitnessClassVerdict("example.com/scoped/s.TestS"); !strings.Contains(reason, "outside this operation's symbol set") {
		t.Fatalf("out-of-set class reason %q; want the refusal", reason)
	}
	if _, err := served.NeverServe([]string{"example.com/scoped/s.TestS"}); err == nil {
		t.Fatal("out-of-set serving refusal answered")
	}
	// A symbol no package can be split from is not scoped by the set:
	// it answers not found, as it does under any load.
	if res, _, _, err := served.ResolveIn(""); err != nil || res != verify.NotFound {
		t.Fatalf("empty symbol: %v, %v; want not found", res, err)
	}
}

// TestServedWithoutASymbolSetAnswersTheWholeTree pins the other side
// of the boundary (REQ-evidence-resolution-freshness): a backend built
// for the declaration-reading roles names no symbol set, its child is
// the whole tree, and every role answers any symbol as the tree
// declares it — the binding, pinning, and retargeting tools construct
// this way and resolve through it.
func TestServedWithoutASymbolSetAnswersTheWholeTree(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's types")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":      "module example.com/scoped\n\ngo 1.26\n",
		"q/q.go":      "package q\n\nfunc F() int { return 1 }\n",
		"s/s.go":      "package s\n\nfunc Unrelated() {}\n",
		"s/s_test.go": "package s\n\nimport \"testing\"\n\nfunc TestS(t *testing.T) {}\n",
	})
	served, err := NewServed(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	for _, symbol := range []string{"example.com/scoped/q.F", "example.com/scoped/s.Unrelated"} {
		if res, _, _, err := served.ResolveIn(symbol); err != nil || res != verify.Resolved {
			t.Fatalf("%s: %v, %v; want resolved through the whole-tree child", symbol, res, err)
		}
		if pkg, err := served.SymbolPackage(symbol); err != nil || pkg == "" {
			t.Fatalf("%s: package %q, %v", symbol, pkg, err)
		}
	}
	if _, reason := served.WitnessClassVerdict("example.com/scoped/s.TestS"); strings.Contains(reason, "outside") {
		t.Fatalf("class refused under no set: %q", reason)
	}
	if _, err := served.NeverServe([]string{"example.com/scoped/s.TestS"}); err != nil {
		t.Fatalf("serving refusal errored under no set: %v", err)
	}
	if res, _, _, err := served.ResolveIn("example.com/scoped/nowhere.Gone"); err != nil || res != verify.NotFound {
		t.Fatalf("undeclared symbol: %v, %v; want not found", res, err)
	}
}

// TestGeneratedVerdictReadsADependencyDeclaringFile pins the
// generated-file verdict past the load set (REQ-evidence-generated-code):
// a method promoted from a type a dependency module declares in a
// generated file — held as export data under the whole-tree load —
// judges generated from the declaring file's own header, so the verdict
// no longer depends on which packages the load carried with syntax.
func TestGeneratedVerdictReadsADependencyDeclaringFile(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-generated-code")
	if testing.Short() {
		t.Skip("loads a fixture module's types")
	}
	neutralAmbient(t)
	root := writeModule(t, map[string]string{
		"gen/go.mod":  "module example.com/gen\n\ngo 1.26\n",
		"gen/gen.go":  "// Code generated by tool. DO NOT EDIT.\n\npackage gen\n\ntype Base struct{}\n\nfunc (Base) Gen() int { return 1 }\n",
		"main/go.mod": "module example.com/main\n\ngo 1.26\n\nrequire example.com/gen v0.0.0\n\nreplace example.com/gen => ../gen\n",
		"main/q/q.go": "package q\n\nimport \"example.com/gen\"\n\ntype T struct{ gen.Base }\n\nfunc F() int { return T{}.Gen() }\n",
	})
	whole, err := NewOwned(context.Background(), filepath.Join(root, "main"))
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	if res, _, _, err := whole.ResolveIn("example.com/main/q.T.Gen"); err != nil || res != verify.GeneratedFile {
		t.Fatalf("promoted method from a dependency's generated file resolved %v, %v; want the generated-file verdict", res, err)
	}
	if res, _, _, err := whole.ResolveIn("example.com/main/q.F"); err != nil || res != verify.Resolved {
		t.Fatalf("plain function resolved %v, %v", res, err)
	}
	// A dependency's own symbol is outside the tree: the whole-tree load
	// answers not found, and a scoped load asked for that package must
	// answer the same — no member owns it, so nobody loads it, rather
	// than go list resolving it out of the module graph as a root.
	if res, _, _, err := whole.ResolveIn("example.com/gen.Base.Gen"); err != nil || res != verify.NotFound {
		t.Fatalf("dependency symbol under the whole tree: %v, %v; want not found", res, err)
	}
	scoped, err := NewOwnedScoped(context.Background(), filepath.Join(root, "main"), []string{"example.com/gen"})
	if err != nil {
		t.Fatal(err)
	}
	defer scoped.Close()
	if res, _, _, err := scoped.ResolveIn("example.com/gen.Base.Gen"); err != nil || res != verify.NotFound {
		t.Fatalf("dependency symbol under a scoped load naming its package: %v, %v; want not found as the whole tree answers", res, err)
	}
}

// TestScopedLoadOwnsPatternsPerWorkspaceMember pins the scoped load over
// a workspace (REQ-evidence-resolution-freshness): each pattern loads
// from the member whose module owns it, once, so the load holds no
// duplicate of a package per member.
func TestScopedLoadOwnsPatternsPerWorkspaceMember(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture workspace's types")
	}
	neutralAmbient(t)
	root := writeModule(t, map[string]string{
		"go.work":  "go 1.26\n\nuse (\n\t./a\n\t./b\n)\n",
		"a/go.mod": "module example.com/a\n\ngo 1.26\n",
		"a/p/p.go": "package p\n\nfunc A() int { return 1 }\n",
		"b/go.mod": "module example.com/b\n\ngo 1.26\n",
		"b/p/p.go": "package p\n\nfunc B() int { return 2 }\n",
	})
	b, err := newContext(context.Background(), root, []string{"example.com/a/p", "example.com/b/p"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, pkg := range b.pkgs {
		if !strings.HasSuffix(pkg.ID, ".test") && !strings.Contains(pkg.ID, "[") {
			seen[pkg.PkgPath]++
		}
	}
	for _, path := range []string{"example.com/a/p", "example.com/b/p"} {
		if seen[path] != 1 {
			t.Fatalf("%s loaded %d times (%v); want once", path, seen[path], seen)
		}
	}
	for symbol, want := range map[string]verify.Resolution{"example.com/a/p.A": verify.Resolved, "example.com/b/p.B": verify.Resolved} {
		if res, _, _, err := b.ResolveIn(symbol); err != nil || res != want {
			t.Fatalf("%s resolved %v, %v", symbol, res, err)
		}
	}
}

// TestServedRecordsNothingThatMovedDuringTheRun pins the straddle check:
// a symbol edited between the child's snapshot and the close is not
// recorded, so the next run resolves it typed again instead of serving
// a stale shape under a moved fingerprint.
func TestServedRecordsNothingThatMovedDuringTheRun(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	symbols := servedSymbols[:2]
	first, err := NewServed(ctx, dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	cold := ask(t, first, "example.com/served/p.F")
	ask(t, first, "example.com/served/p.H")
	// The tree moves after the child's snapshot answered F.
	p := filepath.Join(dir, "p", "p.go")
	src, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(replaceOnce(t, string(src), "func F(n int) int { return H(n) * 2 }", "func F(n int64) int { return H(int(n)) * 2 }")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for _, rec := range resolutioncache.Load(dir) {
		if rec.Symbol == "example.com/served/p.F" || rec.Symbol == "example.com/served/p.H" {
			t.Fatalf("a symbol that moved during the run was recorded: %+v", rec)
		}
	}
	c := countSpawns(t)
	second, err := NewServed(ctx, dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	if got := ask(t, second, "example.com/served/p.F"); got.shape == cold.shape || c.snapshot().child != 1 {
		t.Fatalf("after the mid-run move: shape %s (cold %s) with %d children; want the new shape, typed", got.shape[:8], cold.shape[:8], c.snapshot().child)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// keep records imported for the fixture helpers shared with served_test.
var _ = records.Store{}
