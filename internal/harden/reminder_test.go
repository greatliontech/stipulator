package harden

import (
	"fmt"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/stipulate"
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
	stipulate.Covers(t, "REQ-harden-coverage-reminder")
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

	// A sheet current for Add (matching body, witness, operators, toolchain)
	// and a stale sheet for Weak (wrong body hash).
	sheet := fmt.Sprintf(`records {
  backend: "go"
  symbol: %q
  body_hash: %q
  witness_pins { symbol: %q body_hash: %q }
  operators: %q
  toolchain: %q
}
records {
  backend: "go"
  symbol: %q
  body_hash: "0000000000000000000000000000000000000000000000000000000000000000"
  operators: %q
  toolchain: %q
}
`,
		add, hashOf(t, backend, add),
		"example.com/fixture/lib.TestAdd", hashOf(t, backend, "example.com/fixture/lib.TestAdd"),
		golang.OperatorSet, tc,
		weak, golang.OperatorSet, tc)

	spec, store := fixture(t, map[string]string{".stipulator/hardening/r.textproto": sheet})

	covered := []string{"REQ-h-strong", "REQ-h-weak", "REQ-h-shared", "REQ-h-typed"}
	rep, err := CoverageReminder(spec, store, backend, tc, covered)
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
	rep2, err := CoverageReminder(spec2, store2, backend, tc, []string{"REQ-h-strong"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Entries) != 1 || rep2.Entries[0].Symbol != add || rep2.Entries[0].State != Missing {
		t.Fatalf("missing case: %+v", rep2.Entries)
	}

	// The displayed requirements are the covered subset: Weak implements
	// REQ-h-weak and REQ-h-shared, but covering only REQ-h-weak lists just
	// that one — even though freshness still uses the full witness union.
	rep4, err := CoverageReminder(spec2, store2, backend, tc, []string{"REQ-h-weak"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep4.Entries) != 1 || rep4.Entries[0].Symbol != weak ||
		len(rep4.Entries[0].Requirements) != 1 || rep4.Entries[0].Requirements[0] != "REQ-h-weak" {
		t.Fatalf("covered-subset filter: %+v", rep4.Entries)
	}

	// A covered body with no witness cannot be hardened.
	rep3, err := CoverageReminder(spec2, store2, backend, tc, []string{"REQ-h-untested"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep3.Entries) != 1 || rep3.Entries[0].Hardenable {
		t.Fatalf("witness-less body should be non-hardenable: %+v", rep3.Entries)
	}
	if got := rep.Hardenable(); len(got) != 1 || got[0].Symbol != weak {
		t.Fatalf("Hardenable() = %+v, want [Weak]", got)
	}

	// Each pin is load-bearing: an Add sheet matching every pin but one is
	// stale, so Add is reminded. The witness case moves the witness body
	// hash; the others move operators or toolchain.
	addBody := hashOf(t, backend, add)
	testAdd := "example.com/fixture/lib.TestAdd"
	addWit := hashOf(t, backend, testAdd)
	for _, c := range []struct {
		name, wit, ops, chain string
	}{
		{"wrong-witness-hash", "deadbeef", golang.OperatorSet, tc},
		{"wrong-operators", addWit, "go/999", tc},
		{"wrong-toolchain", addWit, golang.OperatorSet, "other/toolchain"},
	} {
		s := fmt.Sprintf(`records {
  backend: "go"
  symbol: %q
  body_hash: %q
  witness_pins { symbol: %q body_hash: %q }
  operators: %q
  toolchain: %q
}
`, add, addBody, testAdd, c.wit, c.ops, c.chain)
		sp, st := fixture(t, map[string]string{".stipulator/hardening/r.textproto": s})
		r, err := CoverageReminder(sp, st, backend, tc, []string{"REQ-h-strong"})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Entries) != 1 || r.Entries[0].Symbol != add || r.Entries[0].State != Stale {
			t.Fatalf("%s: want Add stale, got %+v", c.name, r.Entries)
		}
	}
}
