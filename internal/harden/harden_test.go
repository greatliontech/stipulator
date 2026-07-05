package harden

import (
	"context"
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

const doc = "# T\n\n**REQ-h-strong** (behavior): It MUST add.\n\n**REQ-h-weak** (behavior): It MUST weaken.\n\n**REQ-h-untested** (behavior): It MUST float.\n"

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

// TestPlanScope pins REQ-harden-scope: requirement and symbol filters
// narrow the targets; a target with no bound tests is reported, never
// silently dropped.
func TestPlanScope(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-scope")
	spec, store := fixture(t, nil)

	all := Plan(spec, store, nil, nil)
	if len(all) != 3 {
		t.Fatalf("targets = %d, want 3: %+v", len(all), all)
	}
	byReq := Plan(spec, store, []string{"REQ-h-strong"}, nil)
	if len(byReq) != 1 || byReq[0].Symbol != "example.com/fixture/lib.Add" {
		t.Fatalf("req scope: %+v", byReq)
	}
	bySym := Plan(spec, store, nil, []string{"example.com/fixture/lib.Weak"})
	if len(bySym) != 1 || bySym[0].Requirement != "REQ-h-weak" {
		t.Fatalf("symbol scope: %+v", bySym)
	}
	for _, tgt := range all {
		if tgt.Requirement == "REQ-h-untested" && (len(tgt.TestPkgs) != 0 || tgt.RunRegex != "") {
			t.Fatalf("untested target grew killers: %+v", tgt)
		}
	}
}

// TestRunAndRecords is the end-to-end pin for REQ-harden-mutation,
// REQ-harden-records, and REQ-harden-exploration: survivors are findings
// in the report; kill-sheets pin the body hash; a matching body hash is
// reused as cache; and the only writes are under .stipulator/hardening/.
func TestRunAndRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	stipulate.Covers(t, "REQ-harden-records", "REQ-harden-exploration")
	spec, store := fixture(t, nil)
	backend, err := golang.New("../backends/golang/testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	targets := Plan(spec, store, []string{"REQ-h-strong", "REQ-h-weak", "REQ-h-untested"}, nil)
	rep, err := Run(context.Background(), "../backends/golang/testdata/fixturemod", backend, store, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}

	byReq := map[string]Result{}
	for _, r := range rep.Results {
		byReq[r.Requirement] = r
	}
	if r := byReq["REQ-h-strong"]; r.Killed != r.Mutants || r.Mutants == 0 || len(r.Survivors) != 0 {
		t.Fatalf("strong: %+v", r)
	}
	if r := byReq["REQ-h-weak"]; len(r.Survivors) == 0 {
		t.Fatalf("weak produced no survivors: %+v", r)
	}
	if r := byReq["REQ-h-untested"]; !r.SkippedNoTest {
		t.Fatalf("untested not reported skipped: %+v", r)
	}

	// Records: only under the hardening dir, body hash pinned, survivors
	// carried.
	updates := rep.Records(store)
	if len(updates) != 2 { // strong + weak; skipped writes nothing
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
		}
	}

	// Cache: reload with the written records; matching body hash reruns
	// nothing; force reruns.
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
		if r.SkippedNoTest {
			continue
		}
		if !r.Cached {
			t.Fatalf("matching body hash not reused: %+v", r)
		}
	}
	if len(rep2.Records(store2)) != 0 {
		t.Fatal("cached run rewrote records")
	}
	if sv := byReq["REQ-h-weak"].Survivors; len(sv) > 0 {
		for _, r := range rep2.Results {
			if r.Requirement == "REQ-h-weak" && len(r.Survivors) != len(sv) {
				t.Fatalf("cached survivors lost: %+v", r)
			}
		}
	}
}
