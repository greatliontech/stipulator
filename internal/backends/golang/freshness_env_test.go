package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// simpleModule writes a self-contained module with one passing test
// that also reads its module root listing and a .git file — the
// volatile observations the witness path must never record.
func simpleModule(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/envfix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real (empty) repository: go's VCS stamping probes it, and the
	// fixture test reads it — the volatile observation under test. Global
	// and system git config are pinned away: a child process's config
	// reads are outside the testlog, so ambient config must not be able
	// to shape recorded fixture state. The fixture test also spawns a
	// child process of its own — an effect no positive observation proof
	// covers — so its record stays unpublishable and the run's cache
	// shrinkage stays a visible, testable number.
	git := exec.Command("git", "init", "-q", tmp)
	git.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := git.CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
	testSource := `package envfix

import (
	"os"
	"os/exec"
	"testing"
)

func TestReadsVolatileState(t *testing.T) {
	if _, err := os.ReadDir("."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(".git/HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skip("git unavailable")
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "envfix_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)
	return tmp
}

// TestGoRunWitnessesUnderForeignWorkspace pins the environment seam
// that once made freshness witnesses fail only inside a completed gate:
// the witness invocations pin GOWORK per module, and the analysis
// engines must analyze under the same pinning — an ambient workspace
// pointing at another tree (the outer harness's own, when this run is
// itself a witness) must never leak into the module's go invocations.
//
// Deliberately not //gofresh:pure: the fixture leans on the git
// binary's init behavior, a child-process input no guard covers; the
// witness re-runs every gate.
func TestGoRunWitnessesUnderForeignWorkspace(t *testing.T) {
	// The witness store must never be the user's real one: an
	// un-overridden store poisons this package's own observation and
	// pollutes the host cache (t.Setenv forbids t.Parallel, which these
	// tests drop for hermeticity).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	work, err := filepath.Abs("../../../go.work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(work); err != nil {
		t.Skipf("no outer workspace to leak: %v", err)
	}
	t.Setenv("GOWORK", work)

	tmp := simpleModule(t)
	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded under foreign GOWORK: %s", tr.Degraded)
	}
	if tr.Outcomes["example.com/envfix.TestReadsVolatileState"] == 0 {
		t.Fatalf("fixture test outcome missing: %v", tr.Outcomes)
	}
	// The fixture test spawns a child process — an effect no positive
	// observation proof covers — and carries no //gofresh:pure directive,
	// so its record cannot publish and the shrinkage stays visible as a
	// number. (Its volatile reads alone would no longer refuse: the
	// observation proof covers observed reads and the audited harness
	// failure channel, and the root/VCS exclusion is the spec's accepted
	// assertion.)
	if tr.Uncached != tr.Ran || tr.Uncached == 0 {
		t.Fatalf("uncached = %d with ran = %d; cache shrinkage must be counted", tr.Uncached, tr.Ran)
	}
}

// TestGoRunWitnessesCarriesFailureOutput pins the failure-diagnostic
// merge across the selective run's processes: a red witness must be
// diagnosable from the run that saw it.
//
//gofresh:pure
func TestGoRunWitnessesCarriesFailureOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod": "module example.com/redfix\n\ngo 1.26\n",
		"redfix_test.go": `package redfix

import "testing"

func TestAlwaysRed(t *testing.T) {
	t.Fatal("the diagnostic that must survive the merge")
}
`,
	})
	writeRacePolicy(t, tmp)
	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	key := "example.com/redfix.TestAlwaysRed"
	if tr.Outcomes[key] != 2 {
		t.Fatalf("outcome = %v, want failed", tr.Outcomes[key])
	}
	if !strings.Contains(tr.Failures[key], "the diagnostic that must survive the merge") {
		t.Fatalf("failure diagnostics lost in the merge: %q", tr.Failures[key])
	}
}

// TestGoRunWitnessesServeWidthReadingWitness pins the freshness loop
// for a witness that observes the delivered inner-parallelism width: a
// second identical run serves the record fresh, because revalidation
// recomputes environment digests from the witness-process environment
// (the analysis engine's producer env) rather than the uncapped
// analysis env - which would refuse the record on every run and turn
// the cap into a permanent cache miss for exactly the witnesses that
// notice it (REQ-evidence-witness-freshness, the concurrency clause).
func TestGoRunWitnessesServeWidthReadingWitness(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("runs a race-instrumented witness pass over a temporary module, twice")
	}
	neutralAmbient(t)
	// An ambient GOMAXPROCS at or under the derived width would suppress
	// the injection and make both arms vacuously agree on every host;
	// pinning it wider than any derivable width forces the injection, so
	// the witness discriminates the producer-env revalidation on every
	// host geometry.
	t.Setenv("GOMAXPROCS", strconv.Itoa(2*runtime.GOMAXPROCS(0)))
	files := map[string]string{
		"go.mod":     "module example.com/widthfixture\n\ngo 1.26\n",
		"lib/lib.go": "package lib\n\nfunc Two() int { return 2 }\n",
		"lib/lib_test.go": `package lib

import (
	"os"
	"testing"
)

func TestReadsWidth(t *testing.T) {
	_ = os.Getenv("GOMAXPROCS")
	if Two() != 2 {
		t.Fatal("wrong")
	}
}
`,
	}
	tmp := writeModule(t, files)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, tmp, p)
	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ran != 1 || first.Fresh != 0 {
		t.Fatalf("first run: ran=%d fresh=%d uncached=%d, want 1 ran", first.Ran, first.Fresh, first.Uncached)
	}
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fresh != 1 || second.Ran != 0 {
		t.Fatalf("second run: ran=%d fresh=%d uncached=%d - a width-reading witness must serve under the producer env", second.Ran, second.Fresh, second.Uncached)
	}
}
