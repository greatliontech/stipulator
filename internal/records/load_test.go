package records

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestHardeningLoadToleratesUnknownField pins REQ-harden-records' load rule
// for the exploration store: a kill-sheet written before a pin existed —
// here a pre-content-pin sheet carrying the retired `witnesses` field —
// loads by discarding the unknown field and re-stales (no witness_pins),
// rather than bricking the whole store load. The retired field is the exact
// forward-compat case the tolerance exists for.
func TestHardeningLoadToleratesUnknownField(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-records")
	old := `# proto-message: stipulator.v1.HardeningSet
records {
  backend: "go"
  symbol: "example.com/p.F"
  body_hash: "` + strings.Repeat("0", 64) + `"
  witnesses: "example.com/p.TestF"
  operators: "go/2"
}
`
	store, err := Load(fstest.MapFS{
		HardeningDir + "/f.textproto": {Data: []byte(old)},
	})
	if err != nil {
		t.Fatalf("pre-content-pin sheet did not load: %v", err)
	}
	if len(store.Hardening) != 1 || len(store.Hardening[0].Set.GetRecords()) != 1 {
		t.Fatalf("sheet not loaded: %+v", store.Hardening)
	}
	rec := store.Hardening[0].Set.GetRecords()[0]
	if len(rec.GetWitnessPins()) != 0 {
		t.Fatalf("retired witnesses field re-read as content pins: %+v", rec.GetWitnessPins())
	}
	if rec.GetSymbol() != "example.com/p.F" {
		t.Fatalf("known fields dropped alongside the unknown one: %+v", rec)
	}
}

// TestAuthoritativeStoresStayStrict is the other half of the asymmetry: the
// gate-feeding stores reject an unknown field, because there a typo'd field
// silently drops a claim. Only the exploration store tolerates drift.
func TestAuthoritativeStoresStayStrict(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-records")
	bogus := `bindings {
  requirement_id: "REQ-x"
  backend: "go"
  symbol: "example.com/p.F"
  role: BINDING_ROLE_IMPLEMENTS
  not_a_field: "x"
}
`
	if _, err := Load(fstest.MapFS{
		BindingsDir + "/t.textproto": {Data: []byte(bogus)},
	}); err == nil {
		t.Fatal("binding store accepted an unknown field")
	}
}
