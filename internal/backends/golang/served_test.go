package golang

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
	"strings"
)

// servedModule is the served backend's fixture: a callable that
// references another, a test, and a type — the last no gofresh
// subject, so it resolves typed every run.
func servedModule(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod":                       "module example.com/served\n\ngo 1.26\n",
		"p/p.go":                       "package p\n\n// T is a type: no subject, no record.\ntype T struct{ N int }\n\nfunc H(n int) int { return n + 1 }\n\nfunc F(n int) int { return H(n) * 2 }\n",
		"p/p_test.go":                  "package p\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 4 {\n\t\tt.Fatal(\"F\")\n\t}\n}\n",
		"p/ext_test.go":                "package p_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/served/p\"\n)\n\nfunc TestExt(t *testing.T) {\n\tif p.H(1) != 2 {\n\t\tt.Fatal(\"H\")\n\t}\n}\n",
		".stipulator/policy.textproto": "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
	})
}

type answer struct {
	res       verify.Resolution
	shape     string
	selection string
	pkg       string
	class     verify.WitnessClass
	reason    string
	refusal   string
}

// ask records every field a verification reads for one symbol.
func ask(t *testing.T, s *Served, symbol string) answer {
	t.Helper()
	res, shape, selection, err := s.ResolveIn(symbol)
	if err != nil {
		t.Fatalf("%s: %v", symbol, err)
	}
	pkg, err := s.SymbolPackage(symbol)
	if err != nil {
		t.Fatalf("%s: package: %v", symbol, err)
	}
	class, reason := s.WitnessClassVerdict(symbol)
	refusals, err := s.NeverServe([]string{symbol})
	if err != nil {
		t.Fatalf("%s: never-serve: %v", symbol, err)
	}
	return answer{res, shape, selection, pkg, class, reason, refusals[symbol]}
}

var servedSymbols = []string{"example.com/served/p.F", "example.com/served/p.H", "example.com/served/p.TestF", "example.com/served/p.TestExt", "example.com/served/p.T"}

// TestServedAnswersFromRecordsWithoutTheChild pins the served path: the
// first run resolves typed through the child and publishes records; the
// second run answers every callable's every field identically without
// opening the child, and opens it only when asked about the type, which
// has no record (REQ-evidence-resolution-freshness).
func TestServedAnswersFromRecordsWithoutTheChild(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	c := countSpawns(t)

	first, err := NewServed(ctx, dir, servedSymbols)
	if err != nil {
		t.Fatal(err)
	}
	cold := map[string]answer{}
	for _, symbol := range servedSymbols {
		cold[symbol] = ask(t, first, symbol)
	}
	if c.snapshot().child == 0 {
		t.Fatal("the first run opened no child; the no-child pin below would be vacuous")
	}
	if cold["example.com/served/p.F"].res != verify.Resolved || cold["example.com/served/p.T"].res != verify.Resolved || cold["example.com/served/p.TestF"].class != verify.ExampleWitness {
		t.Fatalf("cold answers: %+v", cold)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	c.reset()
	second, err := NewServed(ctx, dir, servedSymbols)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.ServedCount(); got != 4 {
		t.Fatalf("served %d symbols, want the four callables (the external test included); reasons %v, degraded %v", got, second.Reasons(), second.Degraded())
	}
	for _, symbol := range servedSymbols[:4] {
		if got := ask(t, second, symbol); got != cold[symbol] {
			t.Fatalf("%s served %+v, typed %+v", symbol, got, cold[symbol])
		}
	}
	if got := c.snapshot(); got.child != 0 {
		t.Fatalf("the served run opened %d children for the callables", got.child)
	}
	if got := ask(t, second, "example.com/served/p.T"); got != cold["example.com/served/p.T"] {
		t.Fatalf("type served %+v, typed %+v", got, cold["example.com/served/p.T"])
	}
	if got := c.snapshot(); got.child != 1 {
		t.Fatalf("the type opened %d children, want exactly the one typed resolution", got.child)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestServedReresolvesWhenTheClosureMoves pins the freshness direction:
// a move in a referenced declaration re-resolves the referencing
// callable (its closure moved, whether or not its own shape did), and a
// move in its own signature yields the new shape — never a stale one.
func TestServedReresolvesWhenTheClosureMoves(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	c := countSpawns(t)
	symbols := servedSymbols[:3]
	warm := func() (*Served, map[string]answer) {
		t.Helper()
		s, err := NewServed(ctx, dir, symbols)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]answer{}
		for _, symbol := range symbols {
			got[symbol] = ask(t, s, symbol)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return s, got
	}
	_, cold := warm()
	c.reset()
	if s, got := warm(); s.ServedCount() != 3 || c.snapshot().child != 0 {
		t.Fatalf("warm run served %d with %d children: %+v", s.ServedCount(), c.snapshot().child, got)
	}
	// An edit outside the closures — a new package the symbols never
	// reach — keeps every record served; the closure is file-granular,
	// so any byte of a contributing file re-resolves (gofresh's
	// contract), and an edit elsewhere in the tree does not.
	if err := os.MkdirAll(filepath.Join(dir, "q"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "q", "q.go"), []byte("package q\n\nfunc Q() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.reset()
	if s, _ := warm(); s.ServedCount() != 3 || c.snapshot().child != 0 {
		t.Fatalf("an edit outside the closures re-resolved: served %d, %d children, reasons %v", s.ServedCount(), c.snapshot().child, s.Reasons())
	}
	p := filepath.Join(dir, "p", "p.go")
	src, _ := os.ReadFile(p)
	// H's signature moves: H, F (which references H), and TestF (which
	// reaches both) re-resolve typed; F's shape is unchanged, H's is new.
	moved := string(src)
	moved = replaceOnce(t, moved, "func H(n int) int { return n + 1 }", "func H(n int64) int { return int(n) + 1 }")
	moved = replaceOnce(t, moved, "return H(n) * 2", "return H(int64(n)) * 2")
	if err := os.WriteFile(p, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	c.reset()
	s, got := warm()
	if c.snapshot().child == 0 || s.ServedCount() != 0 {
		t.Fatalf("a referenced signature moved: served %d, %d children, reasons %v", s.ServedCount(), c.snapshot().child, s.Reasons())
	}
	if got["example.com/served/p.F"].shape != cold["example.com/served/p.F"].shape || got["example.com/served/p.H"].shape == cold["example.com/served/p.H"].shape {
		t.Fatalf("shapes after H moved: F %s→%s, H %s→%s", cold["example.com/served/p.F"].shape[:8], got["example.com/served/p.F"].shape[:8], cold["example.com/served/p.H"].shape[:8], got["example.com/served/p.H"].shape[:8])
	}
	// The republished records serve the moved tree.
	c.reset()
	if s, again := warm(); s.ServedCount() != 3 || c.snapshot().child != 0 || again["example.com/served/p.H"] != got["example.com/served/p.H"] {
		t.Fatalf("after republication: served %d, %d children, H %+v", s.ServedCount(), c.snapshot().child, again["example.com/served/p.H"])
	}
}

// TestServedNarrowsVanishedSubjects pins the narrowing: a recorded
// callable deleted from the source refuses the view by name, is dropped
// from the served set, and resolves typed as not found.
func TestServedNarrowsVanishedSubjects(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	symbols := servedSymbols[:3]
	s, err := NewServed(ctx, dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range symbols {
		ask(t, s, symbol)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "p", "p.go")
	src, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(replaceOnce(t, string(src), "func F(n int) int { return H(n) * 2 }", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "p_test.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err = NewServed(ctx, dir, symbols)
	if err != nil {
		t.Fatalf("a vanished subject failed the served run: %v", err)
	}
	if why := s.Reasons()["example.com/served/p.F"]; why != "no longer declared in the selected source" || s.Degraded() != nil {
		t.Fatalf("vanished F: reason %q, degraded %v", why, s.Degraded())
	}
	if res, _, _, err := s.ResolveIn("example.com/served/p.F"); err != nil || res != verify.NotFound {
		t.Fatalf("vanished F resolved %v, %v", res, err)
	}
	if res, _, _, err := s.ResolveIn("example.com/served/p.H"); err != nil || res != verify.Resolved {
		t.Fatalf("H after F vanished: %v, %v (reason %q)", res, err, s.Reasons()["example.com/served/p.H"])
	}
	t.Logf("H after F vanished: served %v, reason %q", s.ServedCount() == 1, s.Reasons()["example.com/served/p.H"])
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	i := indexOf(s, old)
	if i < 0 {
		t.Fatalf("fixture lacks %q", old)
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestWitnessRunClassifiesFromRecordsWithoutTheChild pins the restaged
// classification: a witness run over a served backend whose records
// are fresh asks no child for the random-seeded classification — the
// records carry each witness's serving refusal — so a warm run pays no
// typed load at all (REQ-evidence-resolution-freshness).
func TestWitnessRunClassifiesFromRecordsWithoutTheChild(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a fixture module")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	c := countSpawns(t)
	run := func() (*Served, *verify.TestRun) {
		t.Helper()
		pc, err := LoadCapture(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		symbols, err := OperationSymbols(ctx, &records.Store{}, pc)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewServed(ctx, dir, symbols)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := RunWitnessesPolicy(ctx, pc, s)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		return s, tr
	}
	if _, cold := run(); cold.Ran != 2 || c.snapshot().child == 0 {
		t.Fatalf("cold run ran %d witnesses with %d children; the warm pin below would be vacuous", cold.Ran, c.snapshot().child)
	}
	c.reset()
	s, warm := run()
	if warm.Fresh != 2 || warm.Ran != 0 {
		t.Fatalf("warm run served %d, ran %d; want both witnesses served", warm.Fresh, warm.Ran)
	}
	if got := c.snapshot().child; got != 0 || s.ServedCount() != 2 {
		t.Fatalf("warm run opened %d children and served %d symbols; want the classification from records", got, s.ServedCount())
	}
}

// TestGCResolutionsKeepsBoundAndWitnessSymbols pins the store cleanup:
// a record whose symbol no binding names and no witness subject carries
// is removed; bound symbols and the captured policy's witness subjects
// keep theirs (REQ-evidence-store-gc).
func TestGCResolutionsKeepsBoundAndWitnessSymbols(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	if testing.Short() {
		t.Skip("captures a fixture module's policy")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	for _, sym := range []string{"example.com/served/p.F", "example.com/served/p.TestF", "example.com/served/p.Orphan"} {
		if err := resolutioncache.Install(dir, resolutioncache.Record{Selection: "default", Symbol: sym, Fingerprint: fingerprintFor("a"), Resolution: "resolved", Package: "example.com/served/p"}); err != nil {
			t.Fatal(err)
		}
	}
	store := &records.Store{Bindings: []records.BindingFile{{Path: "b", Set: bindingSet("example.com/served/p.F")}}}
	pc, err := LoadCapture(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	removed, kept, err := GCResolutions(ctx, dir, store, pc)
	if err != nil || removed != 1 || kept != 2 {
		t.Fatalf("gc removed %d kept %d err %v; want the orphan alone removed, the bound symbol and the witness kept", removed, kept, err)
	}
	for _, rec := range resolutioncache.Load(dir) {
		if rec.Symbol == "example.com/served/p.Orphan" {
			t.Fatal("the orphan record survived")
		}
	}
}

func fingerprintFor(closure string) witnesscache.Fingerprint {
	return witnesscache.Fingerprint{
		MaximalClosure: strings.Repeat(closure, 32), TestVariantClosure: strings.Repeat("b", 32),
		Toolchain: "go1.27.0", BuildConfig: strings.Repeat("c", 32), ResultKind: gofresh.CodeResult,
	}
}

func bindingSet(symbols ...string) *stipulatorv1.BindingSet {
	set := &stipulatorv1.BindingSet{}
	var bindings []*stipulatorv1.Binding
	for _, sym := range symbols {
		b := &stipulatorv1.Binding{}
		b.SetRequirementId("REQ-x")
		b.SetBackend("go")
		b.SetSymbol(sym)
		b.SetRole(stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)
		bindings = append(bindings, b)
	}
	set.SetBindings(bindings)
	return set
}

// A duplicated identity — two record files of one selection and symbol,
// which a partial install can leave — serves its newest: the records
// come most recently installed first and the first per subject is
// kept, so an older stale twin never displaces the current record into
// a typed resolution (REQ-evidence-record-store-layout).
//
//gofresh:pure
func TestServedTakesTheNewestOfADuplicatedIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	ctx := context.Background()
	c := countSpawns(t)

	first, err := NewServed(ctx, dir, servedSymbols)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range servedSymbols {
		ask(t, first, symbol)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	var current resolutioncache.Record
	for _, rec := range resolutioncache.Load(dir) {
		if rec.Symbol == "example.com/served/p.F" {
			current = rec
		}
	}
	if current.Symbol == "" {
		t.Fatal("the first run published no record for p.F")
	}
	twin := current
	twin.Fingerprint.MaximalClosure = strings.Repeat("0", 32)
	twin.Shape = "stale twin"
	store, err := resolutioncache.StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"version": 1, "selection": twin.Selection, "symbol": twin.Symbol, "fingerprint": twin.Fingerprint,
		"resolution": twin.Resolution, "shape": twin.Shape, "package": twin.Package,
		"witnessClass": twin.WitnessClass, "witnessClassReason": twin.WitnessClassReason,
	})
	if err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(store, recordstore.Name([]string{twin.Selection, twin.Symbol}, twin.Fingerprint))
	if err := os.WriteFile(older, data, 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
	if got := len(resolutioncache.Load(dir)); got != 5 {
		t.Fatalf("the store loads %d records, want the four published and the twin", got)
	}

	c.reset()
	second, err := NewServed(ctx, dir, servedSymbols)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.ServedCount(); got != 4 {
		t.Fatalf("served %d symbols, want four: the twin displaced p.F (reasons %v)", got, second.Reasons())
	}
	if got := ask(t, second, "example.com/served/p.F"); got.shape == "stale twin" {
		t.Fatalf("p.F served the older twin: %+v", got)
	}
	if got := c.snapshot(); got.child != 0 {
		t.Fatalf("the served run opened %d children", got.child)
	}
}
