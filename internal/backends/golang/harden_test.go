package golang

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/greatliontech/stipulator/stipulate/structural"
)

// TestBodyHash pins the body-hash contract: stable across resolutions,
// distinct across distinct bodies, identical for identical bodies, and
// insensitive to formatting because it hashes canonical text.
func TestBodyHash(t *testing.T) {
	stipulate.Covers(t, "REQ-go-body-hash")
	b := fixtureBackend(t)
	h1, err := b.BodyHash("example.com/fixture/lib.Add")
	if err != nil {
		t.Fatal(err)
	}
	if len(h1) != 64 {
		t.Fatalf("body hash %q", h1)
	}
	h2, err := b.BodyHash("example.com/fixture/lib.Add")
	if err != nil || h1 != h2 {
		t.Fatalf("unstable: %v %s %s", err, h1, h2)
	}
	hw, err := b.BodyHash("example.com/fixture/lib.Weak")
	if err != nil || hw == h1 {
		t.Fatalf("distinct bodies share a hash: %v", err)
	}
	// Shape hash and body hash version different things: F's shape.
	_, shape, err := b.Resolve("example.com/fixture/lib.Add")
	if err != nil || shape == h1 {
		t.Fatalf("body hash equals shape hash: %v", err)
	}
}

// TestVacuity pins the vacuity resolution: assertion-free tests are
// vacuous; failing calls, helper delegation, and fuzz delegation are not.
func TestVacuity(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-vacuity")
	b := fixtureBackend(t)
	cases := []struct {
		symbol string
		want   bool
	}{
		{"example.com/fixture/lib.TestVacuous", true},
		{"example.com/fixture/lib.TestAdd", false},
		{"example.com/fixture/lib.TestWitPass", false},
	}
	for _, c := range cases {
		got, err := b.Vacuous(c.symbol)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("Vacuous(%s) = %v, want %v", c.symbol, got, c.want)
		}
	}
	self := backend // the repo's own tree
	got, err := self.Vacuous(mod + "/internal/canon.FuzzTextProjection")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("fuzz target read as vacuous: f.Fuzz delegation missed")
	}
}

// TestMutants pins the operator set and determinism: sites in source
// order, budget respected, identical runs identical.
func TestMutants(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-operators")
	b := fixtureBackend(t)
	ms, err := b.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]bool{}
	for _, m := range ms {
		ops[m.Operator] = true
		if m.Position == "" || len(m.Source) == 0 {
			t.Fatalf("incomplete mutant: %+v", m)
		}
	}
	for _, want := range []string{"== -> !=", "negate condition", "zero return"} {
		if !ops[want] {
			t.Fatalf("operator %q missing: %v", want, ops)
		}
	}
	capped, err := b.Mutants("example.com/fixture/lib.Add", 2)
	if err != nil || len(capped) != 2 {
		t.Fatalf("budget ignored: %d %v", len(capped), err)
	}
	again, err := b.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil || len(again) != len(ms) {
		t.Fatalf("nondeterministic: %d vs %d", len(again), len(ms))
	}
	for i := range ms {
		if ms[i].Operator != again[i].Operator || ms[i].Position != again[i].Position {
			t.Fatal("mutant order not deterministic")
		}
	}
}

// TestRunMutantOutcomes pins the overlay runner end to end: a pinned-down
// body kills every mutant, an untested branch yields survivors, and the
// tree is never touched.
func TestRunMutantOutcomes(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-mutation")
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	b := fixtureBackend(t)
	dir := "testdata/fixturemod"

	run := func(symbol, regex string) (killed, survived int, survivors []Mutant) {
		ms, err := b.Mutants(symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ms {
			out, err := RunMutant(context.Background(), dir, m, []string{"example.com/fixture/lib"}, regex, 60*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			switch out {
			case MutantKilled:
				killed++
			case MutantSurvived:
				survived++
				survivors = append(survivors, m)
			}
		}
		return
	}

	killed, survived, _ := run("example.com/fixture/lib.Add", "^TestAdd$")
	if survived != 0 || killed == 0 {
		t.Fatalf("Add: killed=%d survived=%d — the pinned body should kill all", killed, survived)
	}
	_, survived, survivors := run("example.com/fixture/lib.Weak", "^TestWeak$")
	if survived == 0 {
		t.Fatal("Weak: the untested branch produced no survivors")
	}
	for _, s := range survivors {
		if !strings.HasPrefix(s.Position, "lib.go:") {
			t.Fatalf("survivor position not file-anchored: %s", s.Position)
		}
	}
}

func fixtureBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := New("testdata/fixturemod")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWitnessClassProof pins the proof class: a test invoking the
// structural library scores as an analyzer proof — resolved from the
// body, outranking property and example — and nothing outside a runnable
// test ever does.
func TestWitnessClassProof(t *testing.T) {
	stipulate.Covers(t, "REQ-go-structural-provers")
	if got := backend.WitnessClass(mod + "/internal/arch.TestCoreNeverImportsBackends"); got != verify.AnalyzerProof {
		t.Fatalf("structural test classified %v", got)
	}
	if got := backend.WitnessClass(mod + "/internal/corpus.TestLoadManifest"); got == verify.AnalyzerProof {
		t.Fatal("ordinary test classified as proof")
	}
	notATest(t)
	if got := backend.WitnessClass(mod + "/internal/backends/golang.notATest"); got == verify.AnalyzerProof {
		t.Fatal("plain function classified as proof; it never runs in a witness run")
	}
}

// notATest invokes the structural library outside any runnable test: the
// classifier must never score it as a proof, because go test never
// executes it.
func notATest(tb testing.TB) {
	structural.NoImport(tb, "github.com/greatliontech/stipulator/internal/canon", "os/exec")
}
