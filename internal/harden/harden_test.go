package harden

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

const doc = "# T\n\n**REQ-h-strong** (behavior): It MUST add.\n\n**REQ-h-weak** (behavior): It MUST weaken.\n\n**REQ-h-untested** (behavior): It MUST float.\n\n**REQ-h-shared** (behavior): It MUST share.\n\n**REQ-h-typed** (behavior): It MUST type.\n"

func fixture(t *testing.T, extra map[string]string) (*stipulatorv1.Spec, *records.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
		".stipulator/bindings/h.textproto": {Data: []byte(`bindings {
  requirement_id: "REQ-h-strong"
  backend: "go"
  symbol: "example.com/fixture/lib.Add"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-strong"
  backend: "go"
  symbol: "example.com/fixture/lib.TestAdd"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-weak"
  backend: "go"
  symbol: "example.com/fixture/lib.Weak"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-weak"
  backend: "go"
  symbol: "example.com/fixture/lib.TestWeak"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-untested"
  backend: "go"
  symbol: "example.com/fixture/lib.F"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-shared"
  backend: "go"
  symbol: "example.com/fixture/lib.Weak"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-shared"
  backend: "go"
  symbol: "example.com/fixture/lib.TestAdd"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-shared"
  backend: "go"
  symbol: "example.com/fixture/plain.TestPlain"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-typed"
  backend: "go"
  symbol: "example.com/fixture/lib.W"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-typed"
  backend: "go"
  symbol: "example.com/fixture/lib.TestAdd"
  role: BINDING_ROLE_TESTS
}
`)},
	}
	for p, c := range extra {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return spec, store
}

// TestPlanScope pins REQ-harden-scope and the union of
// REQ-harden-mutation: one target per symbol; the killer set is the
// union of the witness bindings of every requirement implementing it;
// requirement and symbol filters narrow the targets; a target with no
// bound witnesses is reported, never silently dropped.
func TestPlanScope(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-scope", "REQ-harden-mutation")
	spec, store := fixture(t, nil)

	all := Plan(spec, store, nil, nil)
	if len(all) != 4 { // Add, F, W, Weak — one target per symbol
		t.Fatalf("targets = %d, want 4: %+v", len(all), all)
	}
	byReq := Plan(spec, store, []string{"REQ-h-strong"}, nil)
	if len(byReq) != 1 || byReq[0].Symbol != "example.com/fixture/lib.Add" {
		t.Fatalf("req scope: %+v", byReq)
	}

	// Weak is implemented by two requirements: its killer set is the
	// union of both requirements' witnesses, and the sheet key is the
	// symbol, so the target appears exactly once.
	bySym := Plan(spec, store, nil, []string{"example.com/fixture/lib.Weak"})
	if len(bySym) != 1 {
		t.Fatalf("symbol scope: %+v", bySym)
	}
	weak := bySym[0]
	if got, want := weak.Requirements, []string{"REQ-h-shared", "REQ-h-weak"}; !slices.Equal(got, want) {
		t.Fatalf("shared symbol requirements = %v, want %v", got, want)
	}
	wantWitnesses := []string{
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.TestWeak",
		"example.com/fixture/plain.TestPlain",
	}
	if !slices.Equal(weak.Witnesses, wantWitnesses) {
		t.Fatalf("witness union = %v, want %v", weak.Witnesses, wantWitnesses)
	}
	// Per-package regexes: one union regex would also run same-named
	// non-witness tests in sibling packages, and their kills would read
	// as unattributed.
	wantRuns := []PkgRun{
		{Pkg: "example.com/fixture/lib", RunRegex: "^(TestAdd|TestWeak)$"},
		{Pkg: "example.com/fixture/plain", RunRegex: "^(TestPlain)$"},
	}
	if !slices.Equal(weak.PkgRuns, wantRuns) {
		t.Fatalf("pkg runs = %+v", weak.PkgRuns)
	}
	if got, want := pkgsOf(weak.PkgRuns), []string{"example.com/fixture/lib", "example.com/fixture/plain"}; !slices.Equal(got, want) {
		t.Fatalf("test packages = %v, want %v", got, want)
	}

	// A requirement filter naming either sharer selects the symbol.
	if byShared := Plan(spec, store, []string{"REQ-h-shared"}, nil); len(byShared) != 1 || byShared[0].Symbol != weak.Symbol {
		t.Fatalf("sharer filter: %+v", byShared)
	}

	for _, tgt := range all {
		if tgt.Symbol == "example.com/fixture/lib.F" && (len(tgt.PkgRuns) != 0 || len(tgt.Witnesses) != 0) {
			t.Fatalf("witness-less target grew killers: %+v", tgt)
		}
	}
}

// TestRunAndRecords is the end-to-end pin for REQ-harden-mutation,
// REQ-harden-records, and REQ-harden-exploration: survivors are findings
// in the report; kill-sheets pin the body hash and the witness set; a
// sheet with both pins matching is reused as cache and either pin moving
// re-stales it; and the only writes are under .stipulator/hardening/.
func TestRunAndRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	stipulate.Covers(t, "REQ-harden-mutation", "REQ-harden-records", "REQ-harden-exploration")
	spec, store := fixture(t, nil)
	backend, err := golang.New("../backends/golang/testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	targets := Plan(spec, store, nil, nil)
	rep, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, store, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// The pool must not leak completion order into the report: a
	// wide-pool run over identical inputs is identical to the first.
	repPar, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, store, targets, Options{Jobs: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep, repPar) {
		t.Fatalf("parallel run differs from sequential-order aggregation:\n%+v\n---\n%+v", rep, repPar)
	}

	bySym := map[string]Result{}
	for _, r := range rep.Results {
		bySym[r.Symbol] = r
	}
	if r := bySym["example.com/fixture/lib.Add"]; r.Killed != r.Mutants || r.Mutants == 0 || len(r.Survivors) != 0 {
		t.Fatalf("strong: %+v", r)
	}
	// Weak's union is MIXED — lib's binary links rapid, plain's does not
	// — so this assertion also pins the per-group flag split: were the
	// rapid flag passed to plain's binary, every run would fail and the
	// unreached-branch survivor would read as a false kill.
	if r := bySym["example.com/fixture/lib.Weak"]; len(r.Survivors) == 0 || len(r.Witnesses) != 3 {
		t.Fatalf("weak produced no survivors or lost its union: %+v", r)
	}
	if r := bySym["example.com/fixture/lib.F"]; !r.SkippedNoTest {
		t.Fatalf("witness-less symbol not reported skipped: %+v", r)
	}
	// A type bound as implements has no body: reported skipped, never
	// fatal, never recorded.
	if r := bySym["example.com/fixture/lib.W"]; !r.SkippedNotFunc {
		t.Fatalf("type-shaped implements binding not reported skipped: %+v", r)
	}

	// Records: only under the hardening dir, both pins present, survivors
	// carried.
	updates := rep.Records(store)
	if len(updates) != 2 { // Add + Weak; skipped writes nothing
		t.Fatalf("record files = %d: %v", len(updates), updates)
	}
	for path, content := range updates {
		if !strings.HasPrefix(path, records.HardeningDir+"/") {
			t.Fatalf("kill-sheet outside the hardening store: %s", path)
		}
		set := &stipulatorv1.HardeningSet{}
		if err := prototext.Unmarshal(content, set); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, rec := range set.GetRecords() {
			if len(rec.GetBodyHash()) != 64 {
				t.Fatalf("record without body hash: %v", rec)
			}
			if len(rec.GetWitnesses()) == 0 {
				t.Fatalf("record without witness pin: %v", rec)
			}
		}
	}

	// Cache: reload with the written records; both pins matching reruns
	// nothing.
	files := map[string]string{}
	for p, c := range updates {
		files[p] = string(c)
	}
	_, store2 := fixture(t, files)
	rep2, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, store2, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep2.Results {
		if r.SkippedNoTest || r.SkippedNotFunc {
			continue
		}
		if !r.Cached {
			t.Fatalf("matching pins not reused: %+v", r)
		}
	}
	if len(rep2.Records(store2)) != 0 {
		t.Fatal("cached run rewrote records")
	}
	if sv := bySym["example.com/fixture/lib.Weak"].Survivors; len(sv) > 0 {
		for _, r := range rep2.Results {
			if r.Symbol == "example.com/fixture/lib.Weak" && len(r.Survivors) != len(sv) {
				t.Fatalf("cached survivors lost: %+v", r)
			}
		}
	}

	// Operator-set staleness: a sheet generated by an older operator set
	// re-stales even with the body and witnesses unchanged — it never
	// generated the mutants the engine now would.
	weakPath := records.HardeningPath("example.com/fixture/lib.Weak")
	oldOps := map[string]string{}
	for p, c := range files {
		oldOps[p] = c
	}
	oldOps[weakPath] = strings.ReplaceAll(oldOps[weakPath], `operators: "`+golang.OperatorSet+`"`, `operators: "go/0"`)
	if oldOps[weakPath] == files[weakPath] {
		t.Fatal("operator pin not present in the written sheet")
	}
	_, storeOld := fixture(t, oldOps)
	repOld, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, storeOld, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repOld.Results {
		if r.Symbol == "example.com/fixture/lib.Weak" && r.Cached {
			t.Fatalf("stale operator set reused as cache: %+v", r)
		}
	}

	// Attestation lifecycle: an attested survivor rides a forced rerun
	// while the pins hold, and is shed when the witness pin moves.
	attFS := fstest.MapFS{}
	for p, c := range files {
		attFS[p] = &fstest.MapFile{Data: []byte(c)}
	}
	attested, err := author.Attest(attFS, "example.com/fixture/lib.Weak",
		bySym["example.com/fixture/lib.Weak"].Survivors[0].Position,
		bySym["example.com/fixture/lib.Weak"].Survivors[0].Operator,
		"unreached branch is fixture-deliberate")
	if err != nil {
		t.Fatal(err)
	}
	files[attested.Path] = string(attested.Content)
	_, storeAtt := fixture(t, files)
	repAtt, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, storeAtt, targets, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repAtt.Results {
		if r.Symbol != "example.com/fixture/lib.Weak" {
			continue
		}
		if len(r.Attested) != 1 || r.Attested[0].Reason != "unreached branch is fixture-deliberate" {
			t.Fatalf("attestation did not ride the forced rerun: %+v", r)
		}
	}

	// Position drift: shift the sheet's recorded positions and body
	// anchor up one line, as an edit above the body would have left
	// them. Every pin still holds, so a forced rerun must rebase and
	// keep the attestation at the current position — drift never sheds
	// a disposition.
	drifted := map[string]string{}
	for p, c := range files {
		drifted[p] = c
	}
	drifted[weakPath] = shiftSheetLines(t, drifted[weakPath], -1)
	_, storeDrift := fixture(t, drifted)
	repDrift, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, storeDrift, targets, Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repDrift.Results {
		if r.Symbol != "example.com/fixture/lib.Weak" {
			continue
		}
		if len(r.Attested) != 1 {
			t.Fatalf("position drift shed the attestation: %+v", r)
		}
		if r.Attested[0].Position != bySym["example.com/fixture/lib.Weak"].Survivors[0].Position {
			t.Fatalf("carried attestation not rebased to the current position: %+v", r.Attested)
		}
	}

	// Budget pin: a capped sheet must not answer a request for more
	// mutants than it generated; a request within the cap is served.
	capped := map[string]string{}
	for p, c := range files {
		capped[p] = c
	}
	capped[weakPath] = strings.Replace(capped[weakPath], "  operators:", "  budget: 1\n  operators:", 1)
	if capped[weakPath] == files[weakPath] {
		t.Fatal("budget pin not injected")
	}
	_, storeCap := fixture(t, capped)
	repCap, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, storeCap, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repCap.Results {
		if r.Symbol == "example.com/fixture/lib.Weak" && r.Cached {
			t.Fatalf("capped sheet answered an exhaustive request: %+v", r)
		}
	}
	repCap2, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, storeCap, targets, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repCap2.Results {
		if r.Symbol == "example.com/fixture/lib.Weak" && !r.Cached {
			t.Fatalf("request within the cap not served from cache: %+v", r)
		}
	}

	// A cap that did not bind records an exhaustive sheet: Weak has
	// fewer mutants than this budget, so the sheet must say 0.
	weakOnly := Plan(spec, store, nil, []string{"example.com/fixture/lib.Weak"})
	repLoose, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, store, weakOnly, Options{Force: true, Budget: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got := repLoose.Results[0].Budget; got != 0 {
		t.Fatalf("unbinding cap recorded as %d, want 0 (exhaustive)", got)
	}

	// Witness-set staleness: binding a new witness to Add changes its
	// union, so its sheet re-stales and reruns — and Weak, whose union
	// also moves, sheds its attestation: a new witness set is a new
	// judgment.
	files[".stipulator/bindings/extra.textproto"] = `bindings {
  requirement_id: "REQ-h-strong"
  backend: "go"
  symbol: "example.com/fixture/lib.TestWeak"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-weak"
  backend: "go"
  symbol: "example.com/fixture/panicky.TestShadowed"
  role: BINDING_ROLE_TESTS
}
`
	spec3, store3 := fixture(t, files)
	targets3 := Plan(spec3, store3, nil, nil)
	rep3, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, store3, targets3, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep3.Results {
		switch r.Symbol {
		case "example.com/fixture/lib.Add":
			if r.Cached {
				t.Fatalf("new witness did not re-stale the sheet: %+v", r)
			}
			if len(r.Witnesses) != 2 {
				t.Fatalf("rerun lost the new union: %+v", r)
			}
		case "example.com/fixture/lib.Weak":
			if r.Cached {
				t.Fatalf("moved witness pin reused as cache: %+v", r)
			}
			if len(r.Attested) != 0 {
				t.Fatalf("attestation survived a moved witness pin: %+v", r)
			}
		}
	}
}

// shiftSheetLines rewrites a sheet's body anchor and every recorded
// position by delta lines, simulating a sheet written before an edit
// above the body.
func shiftSheetLines(t *testing.T, sheet string, delta int) string {
	t.Helper()
	set := &stipulatorv1.HardeningSet{}
	if err := prototext.Unmarshal([]byte(sheet), set); err != nil {
		t.Fatal(err)
	}
	for _, rec := range set.GetRecords() {
		rec.SetBodyLine(rec.GetBodyLine() + int32(delta))
		for _, s := range rec.GetSurvivors() {
			pos, ok := shiftPos(s.GetPosition(), delta)
			if !ok {
				t.Fatalf("unshiftable position %q", s.GetPosition())
			}
			s.SetPosition(pos)
		}
		for _, a := range rec.GetAttested() {
			pos, ok := shiftPos(a.GetPosition(), delta)
			if !ok {
				t.Fatalf("unshiftable position %q", a.GetPosition())
			}
			a.SetPosition(pos)
		}
	}
	return string(records.RenderHardening(set.GetRecords()))
}

// TestAttributedKill pins the acceptance rule for kills: a named witness
// (any subtest depth), or the timeout sentinel; an unexpected killer is a
// corrupted measurement.
func TestAttributedKill(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-mutation")
	set := map[string]bool{"example.com/p.TestA": true}
	for _, ok := range []string{"example.com/p.TestA", golang.TimeoutKiller} {
		if err := attributedKill(ok, set); err != nil {
			t.Errorf("attributedKill(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"example.com/p.TestB", "example.com/q.TestA", "", "(bogus sentinel)", "(timeout extra)"} {
		if err := attributedKill(bad, set); err == nil {
			t.Errorf("unexpected killer %q accepted", bad)
		}
	}
}

func pkgsOf(runs []PkgRun) []string {
	var out []string
	for _, r := range runs {
		out = append(out, r.Pkg)
	}
	return out
}
