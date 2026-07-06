package harden

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

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
	if weak.RunRegex != "^(TestAdd|TestPlain|TestWeak)$" {
		t.Fatalf("run regex = %q", weak.RunRegex)
	}
	if got, want := weak.TestPkgs, []string{"example.com/fixture/lib", "example.com/fixture/plain"}; !slices.Equal(got, want) {
		t.Fatalf("test packages = %v, want %v", got, want)
	}

	// A requirement filter naming either sharer selects the symbol.
	if byShared := Plan(spec, store, []string{"REQ-h-shared"}, nil); len(byShared) != 1 || byShared[0].Symbol != weak.Symbol {
		t.Fatalf("sharer filter: %+v", byShared)
	}

	for _, tgt := range all {
		if tgt.Symbol == "example.com/fixture/lib.F" && (len(tgt.TestPkgs) != 0 || tgt.RunRegex != "" || len(tgt.Witnesses) != 0) {
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

	// Witness-set staleness: binding a new witness to Add changes its
	// union, so its sheet re-stales and reruns; Weak's pins still hold
	// and stay cached.
	files[".stipulator/bindings/extra.textproto"] = `bindings {
  requirement_id: "REQ-h-strong"
  backend: "go"
  symbol: "example.com/fixture/lib.TestWeak"
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
			if !r.Cached {
				t.Fatalf("unchanged pins rerun: %+v", r)
			}
		}
	}
}
