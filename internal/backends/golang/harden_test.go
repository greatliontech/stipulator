package golang

import (
	"testing"

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
	// Generic instantiation is still a direct invocation: this test's
	// body calls structural only through Implements[I](...).
	if got := backend.WitnessClass(mod + "/internal/arch.TestBackendSatisfiesVerifierSurfaces"); got != verify.AnalyzerProof {
		t.Fatalf("generic structural invocation classified %v", got)
	}
	if got := backend.WitnessClass(mod + "/internal/backends/golang.TestFieldHelperOnly"); got != verify.ExampleWitness {
		t.Fatalf("structural helper-only test classified %v, want example", got)
	}
	notATest(t)
	if got := backend.WitnessClass(mod + "/internal/backends/golang.notATest"); got == verify.AnalyzerProof {
		t.Fatal("plain function classified as proof; it never runs in a witness run")
	}
}

func TestFieldHelperOnly(t *testing.T) {
	_ = structural.FieldOf[int]("Value")
}

// notATest invokes the structural library outside any runnable test: the
// classifier must never score it as a proof, because go test never
// executes it.
func notATest(tb testing.TB) {
	structural.NoImport(tb, "github.com/greatliontech/stipulator/internal/canon", "os/exec")
}
