package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// writeRacePolicy commits the simplest witnessing policy — one
// race-enabled invocation over the whole module — so a fixture module's
// every test is in-policy for the selective witness runner.
func writeRacePolicy(t *testing.T, dir string) {
	t.Helper()
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	writePolicyRecord(t, dir, p)
}

// TestGoRunWitnessesTestlessPolicyRunsClean pins the empty end of the
// selective surface: a policy whose invocations select packages with no
// runnable tests is a clean empty run — nothing serves, nothing
// executes, nothing degrades — never a fault. A corpus adopting a policy
// before writing its first test must not fail witnessing.
//
//gofresh:pure
func TestGoRunWitnessesTestlessPolicyRunsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("runs policy discovery over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":   "module example.com/empty\n\ngo 1.26\n",
		"empty.go": "package empty\n",
	})
	writeRacePolicy(t, tmp)
	tr, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatalf("testless policy faulted the run: %v", err)
	}
	if tr.Degraded != "" {
		t.Fatalf("testless policy degraded: %s", tr.Degraded)
	}
	if len(tr.Outcomes) != 0 || tr.Ran != 0 || tr.Fresh != 0 || tr.OutsidePolicy != 0 {
		t.Fatalf("testless policy produced evidence: outcomes=%v ran=%d fresh=%d outside=%d",
			tr.Outcomes, tr.Ran, tr.Fresh, tr.OutsidePolicy)
	}
}

// TestGoRunWitnessesMidRunSourceEditNeverPublishes pins the safe
// direction of pre-execution capture: fingerprints pin the tree that
// compiled the binaries, so an edit made while the tests run voids every
// publication — the executed evidence stands, and nothing is recorded
// under a hash the edited tree no longer matches.
//
//gofresh:pure
func TestGoRunWitnessesMidRunSourceEditNeverPublishes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":    "module example.com/mutate\n\ngo 1.26\n",
		"mutate.go": "package mutate\n",
		"mutate_test.go": `package mutate

import (
	"os"
	"testing"
)

func TestMutatesSourceOnce(t *testing.T) {
	if _, err := os.Stat("mutated.once"); !os.IsNotExist(err) {
		return
	}
	source, err := os.ReadFile("mutate.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mutate.go", append(source, []byte("\n// changed during run\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mutated.once", nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	writeRacePolicy(t, tmp)

	run, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if run.Degraded != "" {
		t.Fatalf("mid-run edit degraded the run: %s", run.Degraded)
	}
	if run.Outcomes["example.com/mutate.TestMutatesSourceOnce"] != verify.TestPassed {
		t.Fatalf("executed evidence lost: %v", run.Outcomes)
	}
	if got := witnesscache.Load(tmp); len(got) != 0 {
		t.Fatalf("mid-run source edit published records: %+v", got)
	}
	if run.Ran != 1 || run.Uncached != 1 {
		t.Fatalf("ran=%d uncached=%d, want the voided publication counted", run.Ran, run.Uncached)
	}
}

// TestGoRunWitnessesMidRunRuntimeInputDriftDropsRecord pins the post-run
// fingerprint check over executed subjects' runtime inputs: a recorded
// input that another process of the same run mutated after the subject's
// observation fails the post-run check, so the record is dropped and
// counted uncacheable while the executed evidence stands. The mutation
// rides the isolation pass — a solo re-run that begins only after every
// package process has completed and observed — so the interleaving is
// structural, not scheduled.
//
//gofresh:pure
func TestGoRunWitnessesMidRunRuntimeInputDriftDropsRecord(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/runtime-drift\n\ngo 1.26\n",
		"reader/data.txt": "before",
		"reader/reader_test.go": `package reader

import (
	"os"
	"testing"
)

//gofresh:pure
func TestReads(t *testing.T) {
	if _, err := os.ReadFile("data.txt"); err != nil {
		t.Fatal(err)
	}
}
`,
		// The writer package's red sibling denies the pass, so the write
		// happens in the isolation pass's solo process: only the second
		// invocation — the solo re-run, after every package process has
		// observed — finds its sentinel and mutates the reader's input.
		"writer/writer_test.go": `package writer

import (
	"os"
	"testing"
)

func TestRedFlag(t *testing.T) {
	t.Fatal("deliberately red so the sibling pass is denied and re-runs solo")
}

func TestWritesOnce(t *testing.T) {
	if _, err := os.Stat("seen.once"); os.IsNotExist(err) {
		if err := os.WriteFile("seen.once", nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile("../reader/data.txt", []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	writeRacePolicy(t, tmp)

	run, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if run.Degraded != "" {
		t.Fatalf("runtime-input drift degraded the run: %s", run.Degraded)
	}
	if got := run.Outcomes["example.com/runtime-drift/reader.TestReads"]; got != verify.TestPassed {
		t.Fatalf("reader evidence lost: %v", got)
	}
	if got := run.Outcomes["example.com/runtime-drift/writer.TestWritesOnce"]; got != verify.TestPassed {
		t.Fatalf("solo-isolated writer pass lost: %v", got)
	}
	if got := run.Outcomes["example.com/runtime-drift/writer.TestRedFlag"]; got != verify.TestFailed {
		t.Fatalf("denying red must stand: %v", got)
	}
	if cacheRecord(t, witnesscache.Load(tmp), "example.com/runtime-drift/reader", "TestReads") != nil {
		t.Error("reader record published although its recorded input drifted mid-run")
	}
	if run.Uncached == 0 {
		t.Error("dropped record not counted uncacheable")
	}
}

// fresh fails the calling phase when the freshness path fell back to
// full execution: a degraded run exercises nothing these tests pin, and
// the fault text is the difference between a contract violation and an
// environmental fault.
func fresh(t *testing.T, tr *verify.TestRun, phase string) {
	t.Helper()
	if tr.Degraded != "" {
		t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
	}
}

func sameRegistrationSet(a, b []verify.Registration) bool {
	set := func(rs []verify.Registration) map[verify.Registration]bool {
		m := map[verify.Registration]bool{}
		for _, r := range rs {
			m[r] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	if len(sa) != len(sb) {
		return false
	}
	for r := range sa {
		if !sb[r] {
			return false
		}
	}
	return true
}

// TestGoRunWitnessesServingRoundTrip pins the serving round trip of the
// selective witness runner (REQ-evidence-witness-freshness): the first
// run executes and fingerprints everything, isolating the abort-shadowed
// sibling and the pass denied by a red process into solo outcomes; the
// second serves every proven-equivalent record with identical outcomes
// and registrations while re-executing exactly the subjects no healthy
// process could publish — the aborter, the failing test, and the skip
// recorded inside its red process; and independent source and fixture
// edits then re-stale exactly their affected tests. Every witness is
// race-attributed (REQ-evidence-run-attributes) and every outcome is a
// current-run `go test -json` derivation or its proven-equivalent serve
// (REQ-go-witness).
//
// The test copies its fixture module before running it, so every fixture
// file rides this process's testlog manifest; the child go invocations
// see only those copies, and the toolchain itself is pinned by the
// fingerprint's toolchain guard. That is why the purity assertion below
// is sound.
//
//gofresh:pure
func TestGoRunWitnessesServingRoundTrip(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-witness", "REQ-evidence-run-attributes")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	t.Parallel()
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/freshfixture")); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, first, "first run")
	if first.Fresh != 0 || first.Ran == 0 {
		t.Fatalf("first run: ran=%d fresh=%d, want everything ran", first.Ran, first.Fresh)
	}
	if !first.RaceEnabled {
		t.Fatal("witness run not race-attributed")
	}
	if first.Outcomes["example.com/freshfixture/lib.TestAdd"] != verify.TestPassed {
		t.Fatalf("TestAdd outcome missing: %v", first.Outcomes)
	}
	store, err := witnesscache.StoreDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(store); err != nil || len(entries) == 0 {
		t.Fatalf("witness store not written: %v (%d entries)", err, len(entries))
	}
	// The clean break: nothing writes inside the repository anymore, and
	// a legacy in-repo cache left by an older binary is removed.
	legacy := filepath.Join(tmp, ".stipulator", "cache")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "witnesses.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if witnesscache.Load(tmp) == nil {
		t.Fatal("store round trip lost its records")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy in-repo cache survived: %v", err)
	}

	// The abort-shadowed sibling is unshadowed by its solo isolation
	// re-run within the first run already.
	if first.Outcomes["example.com/freshfixture/panicky.TestShadowed"] != verify.TestPassed {
		t.Fatalf("the shadowed test was not unshadowed by isolation: %v", first.Outcomes["example.com/freshfixture/panicky.TestShadowed"])
	}

	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, second, "second run")
	// Steady state re-executes exactly the subjects no healthy process
	// granted a record: the aborter (its process dies before the testlog
	// flush), the failing test (a red never publishes), and the skip
	// recorded inside that red process. Everything else serves.
	if second.Ran != 3 || second.Fresh != 5 {
		t.Fatalf("steady state: ran=%d fresh=%d, want 3 recordless re-runs and 5 served", second.Ran, second.Fresh)
	}
	if second.Outcomes["example.com/freshfixture/panicky.TestPanics"] != verify.TestFailed {
		t.Fatalf("the aborting test did not re-run red: %v", second.Outcomes["example.com/freshfixture/panicky.TestPanics"])
	}
	if second.Outcomes["example.com/freshfixture/outcomes.TestFail"] != verify.TestFailed {
		t.Fatalf("the failing test did not re-run red: %v", second.Outcomes["example.com/freshfixture/outcomes.TestFail"])
	}
	if second.Outcomes["example.com/freshfixture/outcomes.TestSkip"] != verify.TestSkipped {
		t.Fatalf("the skipped test lost its outcome: %v", second.Outcomes["example.com/freshfixture/outcomes.TestSkip"])
	}
	if second.Outcomes["example.com/freshfixture/lib.TestAdd"] != verify.TestPassed {
		t.Fatalf("served outcome lost: %v", second.Outcomes["example.com/freshfixture/lib.TestAdd"])
	}
	if second.Outcomes["example.com/freshfixture/freader.TestReadsFixture"] != verify.TestPassed {
		t.Fatalf("pure fixture reader not served: %v", second.Outcomes["example.com/freshfixture/freader.TestReadsFixture"])
	}
	if second.Outcomes["example.com/freshfixture/outcomes.TestPass/sub"] != verify.TestPassed {
		t.Fatalf("cached subtest outcome lost: %v", second.Outcomes)
	}
	// Served registrations are the recorded ones — the same set the first
	// run produced, no losses, no fabrications.
	if !sameRegistrationSet(first.Registrations, second.Registrations) {
		t.Fatalf("registration sets differ:\nfirst:  %+v\nsecond: %+v", first.Registrations, second.Registrations)
	}
	// Every first-run outcome survives the second run.
	for k, v := range first.Outcomes {
		if second.Outcomes[k] != v {
			t.Fatalf("outcome %s changed or vanished: %v -> %v", k, v, second.Outcomes[k])
		}
	}

	// Independently break Add's source and the pure reader's observed fixture.
	// Their failed outcomes prove both tests actually re-ran: the closure guard
	// catches the source edit, while the runtime-input guard catches the
	// non-source edit. Untouched packages stay served.
	libPath := filepath.Join(tmp, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), "return a + b", "return a - b", 1)
	if edited == string(src) {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(libPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "freader", "data.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, third, "edit run")
	if third.Ran != 5 || third.Fresh != 3 {
		t.Fatalf("edit run: ran=%d fresh=%d, want the two edited tests plus the three recordless ones re-run", third.Ran, third.Fresh)
	}
	if third.Outcomes["example.com/freshfixture/lib.TestAdd"] != verify.TestFailed {
		t.Fatalf("source-edited test did not re-run red: %v", third.Outcomes)
	}
	if third.Outcomes["example.com/freshfixture/freader.TestReadsFixture"] != verify.TestFailed {
		t.Fatalf("fixture reader did not re-run red: %v", third.Outcomes)
	}
}

// TestGoRunWitnessesSelectsRaceSources pins that freshness analyzes the
// same race-selected sources as the covering race invocation executes
// (REQ-go-race). The default-only declaration's purity assertion must not
// apply to its race-selected counterpart, and an edit to a race-only
// helper must stale the test that reaches it. Each package has one
// selected test, so process isolation permits proof selection; the race
// I/O test still reruns because its diagnostic error path is not covered
// by a positive observation proof.
//
//gofresh:pure
func TestGoRunWitnessesSelectsRaceSources(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-race")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	t.Parallel()
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/racefixture")); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)

	first, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, first, "first run")
	if first.Fresh != 0 || first.Ran != 2 {
		t.Fatalf("first run: ran=%d fresh=%d, want both tests run", first.Ran, first.Fresh)
	}
	second, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, second, "second run")
	if second.Ran != 1 || second.Fresh != 1 {
		t.Fatalf("second run: ran=%d fresh=%d, want the unasserted race I/O test rerun and closure test served", second.Ran, second.Fresh)
	}

	helperPath := filepath.Join(tmp, "raceclosure", "value_race.go")
	src, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), "race-v1", "race-v2", 1)
	if edited == string(src) {
		t.Fatal("race helper edit failed")
	}
	if err := os.WriteFile(helperPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	third, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, third, "race helper edit")
	if third.Ran != 2 || third.Fresh != 0 {
		t.Fatalf("race helper edit: ran=%d fresh=%d, want both tests run", third.Ran, third.Fresh)
	}
	if third.Outcomes["example.com/racefixture/raceclosure.TestRaceClosure"] != verify.TestPassed {
		t.Fatalf("race-selected closure test did not pass after re-witnessing: %v", third.Outcomes)
	}

	fourth, err := RunWitnesses(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, fourth, "post-edit steady state")
	if fourth.Ran != 1 || fourth.Fresh != 1 {
		t.Fatalf("post-edit steady state: ran=%d fresh=%d, want the unasserted race I/O test rerun and recaptured closure test served", fourth.Ran, fourth.Fresh)
	}
}
