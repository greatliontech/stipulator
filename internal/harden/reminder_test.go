package harden

import (
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

const fixtureDir = "../backends/golang/testdata/fixturemod"

func hashOf(t *testing.T, b *golang.Backend, sym string) string {
	t.Helper()
	h, err := b.BodyHash(sym)
	if err != nil {
		t.Fatalf("body hash %s: %v", sym, err)
	}
	return h
}

// TestCoverageReminder pins REQ-harden-coverage-reminder: a covered body with
// a current sheet drops off; one with a moved pin is Stale; one with no sheet
// is Missing; a non-function binding is never reminded; and Hardenable tracks
// whether a body mutator can reach the witnesses.
func TestCoverageReminder(t *testing.T) {
	backend, err := golang.New(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	tc, err := golang.Toolchain(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	add := "example.com/fixture/lib.Add"
	weak := "example.com/fixture/lib.Weak"

	// A finding current for Add (matching body, witness pins, toolchain) and
	// a stale one for Weak (wrong body hash). The engine's document, as the
	// engine writes it.
	findings := []EngineFinding{
		{Symbol: add, BodyHash: hashOf(t, backend, add),
			Oracle:    []OraclePin{{Symbol: "example.com/fixture/lib.TestAdd", Hash: hashOf(t, backend, "example.com/fixture/lib.TestAdd")}},
			Toolchain: tc},
		{Symbol: weak, BodyHash: "0000000000000000000000000000000000000000000000000000000000000000",
			Toolchain: tc},
	}

	spec, store := fixture(t, nil)

	covered := []string{"REQ-h-strong", "REQ-h-weak", "REQ-h-shared", "REQ-h-typed"}
	rep, err := CoverageReminder(spec, store, backend, tc, covered, findings)
	if err != nil {
		t.Fatal(err)
	}
	bySym := map[string]ReminderEntry{}
	for _, e := range rep.Entries {
		bySym[e.Symbol] = e
	}
	// Add has a current sheet -> dropped.
	if _, ok := bySym[add]; ok {
		t.Errorf("Add with a fresh sheet was still reminded")
	}
	// Weak's sheet pin moved -> Stale, and a body mutator can harden it.
	if e, ok := bySym[weak]; !ok {
		t.Errorf("Weak with a moved pin was not reminded")
	} else if e.State != Stale || !e.Hardenable {
		t.Errorf("Weak: state=%q hardenable=%v, want stale+hardenable", e.State, e.Hardenable)
	}
	// The type W (a non-function implements binding) is never reminded.
	if _, ok := bySym["example.com/fixture/lib.W"]; ok {
		t.Errorf("non-function binding W was reminded")
	}

	// With no sheets at all, a covered body is Missing.
	spec2, store2 := fixture(t, nil)
	rep2, err := CoverageReminder(spec2, store2, backend, tc, []string{"REQ-h-strong"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Entries) != 1 || rep2.Entries[0].Symbol != add || rep2.Entries[0].State != Missing {
		t.Fatalf("missing case: %+v", rep2.Entries)
	}

	// The displayed requirements are the covered subset: Weak implements
	// REQ-h-weak and REQ-h-shared, but covering only REQ-h-weak lists just
	// that one — even though freshness still uses the full witness union.
	rep4, err := CoverageReminder(spec2, store2, backend, tc, []string{"REQ-h-weak"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep4.Entries) != 1 || rep4.Entries[0].Symbol != weak ||
		len(rep4.Entries[0].Requirements) != 1 || rep4.Entries[0].Requirements[0] != "REQ-h-weak" {
		t.Fatalf("covered-subset filter: %+v", rep4.Entries)
	}

	// A covered body with no witness cannot be hardened.
	rep3, err := CoverageReminder(spec2, store2, backend, tc, []string{"REQ-h-untested"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep3.Entries) != 1 || rep3.Entries[0].Hardenable {
		t.Fatalf("witness-less body should be non-hardenable: %+v", rep3.Entries)
	}
	if got := rep.Hardenable(); len(got) != 1 || got[0].Symbol != weak {
		t.Fatalf("Hardenable() = %+v, want [Weak]", got)
	}

	// Each pin stipulator can compute is load-bearing: a finding matching
	// every pin but one is stale, so Add is reminded. Operator-set drift is
	// deliberately not judged here — the engine re-measures on its own bump.
	addBody := hashOf(t, backend, add)
	testAdd := "example.com/fixture/lib.TestAdd"
	addWit := hashOf(t, backend, testAdd)
	for _, c := range []struct {
		name, wit, chain string
	}{
		{"wrong-witness-hash", "deadbeef", tc},
		{"wrong-toolchain", addWit, "other/toolchain"},
	} {
		f := []EngineFinding{{Symbol: add, BodyHash: addBody,
			Oracle:    []OraclePin{{Symbol: testAdd, Hash: c.wit}},
			Toolchain: c.chain}}
		sp, st := fixture(t, nil)
		r, err := CoverageReminder(sp, st, backend, tc, []string{"REQ-h-strong"}, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Entries) != 1 || r.Entries[0].Symbol != add || r.Entries[0].State != Stale {
			t.Fatalf("%s: want Add stale, got %+v", c.name, r.Entries)
		}
	}

	// Oracle-set drift re-stales in both directions, even when every present
	// hash matches: a finding with no pins (a witness has since been bound),
	// and a finding with an extra pin (a witness has since been dropped) —
	// the superset direction is exactly what a size check exists to catch.
	for name, oracle := range map[string][]OraclePin{
		"witness added":   nil,
		"witness dropped": {{Symbol: testAdd, Hash: addWit}, {Symbol: "example.com/fixture/lib.TestGone", Hash: "gh"}},
	} {
		f := []EngineFinding{{Symbol: add, BodyHash: addBody, Oracle: oracle, Toolchain: tc}}
		sp, st := fixture(t, nil)
		r, err := CoverageReminder(sp, st, backend, tc, []string{"REQ-h-strong"}, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Entries) != 1 || r.Entries[0].State != Stale {
			t.Fatalf("%s: want Add stale, got %+v", name, r.Entries)
		}
	}
}
