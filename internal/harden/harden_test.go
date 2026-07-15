package harden

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
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
//
//gofresh:pure
func TestPlanScope(t *testing.T) {
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
	wantWit := []string{
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.TestWeak",
		"example.com/fixture/plain.TestPlain",
	}
	if !slices.Equal(weak.Witnesses, wantWit) {
		t.Fatalf("witness union = %+v", weak.Witnesses)
	}

	// A requirement filter naming either sharer selects the symbol.
	if byShared := Plan(spec, store, []string{"REQ-h-shared"}, nil); len(byShared) != 1 || byShared[0].Symbol != weak.Symbol {
		t.Fatalf("sharer filter: %+v", byShared)
	}

	for _, tgt := range all {
		if tgt.Symbol == "example.com/fixture/lib.F" && len(tgt.Witnesses) != 0 {
			t.Fatalf("witness-less target grew killers: %+v", tgt)
		}
	}
}
