package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
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
	tr, err := RunWitnesses(context.Background(), tmp, noSeeding{})
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
	// A declaration, not a comment: the canonical member form ignores
	// comments, so only a member moves the closure.
	if err := os.WriteFile("mutate.go", append(source, []byte("\nvar changedDuringRun int\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("mutated.once", nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	writeRacePolicy(t, tmp)

	run, err := RunWitnesses(context.Background(), tmp, noSeeding{})
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

// TestGoRunWitnessesInputDriftAfterTheUnitReexecutesNextRun pins the
// package unit against a sibling's write (REQ-evidence-witness-freshness,
// REQ-policy-cancellation): the reader's record publishes the moment its
// package completes, carrying the input it read; a sibling package whose
// isolation re-run then rewrites that input cannot touch the published
// record — the writer's own record is refused for writing outside its
// bracket — and the next run finds the reader's recorded input moved and
// re-executes it rather than serving the record. The writer's rewrite is
// gated on the reader's persisted note, so the order is the test's, not
// the scheduler's.
func TestGoRunWitnessesInputDriftAfterTheUnitReexecutesNextRun(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-cancellation")
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
		// happens in the writer's isolation re-run — the second process,
		// which finds its sentinel — and only once the test has seen the
		// reader's unit persist (go.ahead), so the rewrite lands after the
		// reader's record, never inside its process.
		"writer/writer_test.go": `package writer

import (
	"os"
	"testing"
	"time"
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
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat("go.ahead"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reader's unit never persisted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.WriteFile("../reader/data.txt", []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	writeRacePolicy(t, tmp)
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		if strings.HasPrefix(e.GetNote(), "persisted: ") && strings.Contains(e.GetNote(), "example.com/runtime-drift/reader (") {
			if err := os.WriteFile(filepath.Join(tmp, "writer", "go.ahead"), nil, 0o644); err != nil {
				t.Error(err)
			}
		}
	}, progress.WithInterval(time.Hour))
	ctx := progress.NewContext(context.Background(), rep)

	run, err := RunWitnesses(ctx, tmp, noSeeding{})
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
	first := cacheRecord(t, witnesscache.Load(tmp), "example.com/runtime-drift/reader", "TestReads")
	if first == nil {
		t.Fatal("the reader's record did not publish at its unit's completion")
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "reader", "data.txt")); string(b) != "after" {
		t.Fatalf("the writer's re-run never rewrote the input: %q", b)
	}
	if run.Uncached == 0 {
		t.Error("the writer's out-of-bracket write not counted uncacheable")
	}

	// The next run: the reader's recorded input moved, so it re-executes
	// instead of serving, and its record re-derives against the new input.
	second, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Fresh != 0 {
		t.Fatalf("the next run served %d witnesses; the reader's recorded input moved, nothing may serve", second.Fresh)
	}
	rederived := cacheRecord(t, witnesscache.Load(tmp), "example.com/runtime-drift/reader", "TestReads")
	if rederived == nil || rederived.Fingerprint.RuntimeDigest == first.Fingerprint.RuntimeDigest {
		t.Fatalf("the reader's record was not re-derived against the moved input: first %v, second %+v", first.Fingerprint.RuntimeDigest, rederived)
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
	// The witness store must never be the user's real one: an
	// un-overridden store poisons this package's own observation and
	// pollutes the host cache (t.Setenv forbids t.Parallel, which these
	// tests drop for hermeticity).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-witness", "REQ-evidence-run-attributes")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/freshfixture")); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)

	first, err := RunWitnesses(context.Background(), tmp, noSeeding{})
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
	// The store round-trips what the run wrote.
	if witnesscache.Load(tmp) == nil {
		t.Fatal("store round trip lost its records")
	}

	// The abort-shadowed sibling is unshadowed by its solo isolation
	// re-run within the first run already.
	if first.Outcomes["example.com/freshfixture/panicky.TestShadowed"] != verify.TestPassed {
		t.Fatalf("the shadowed test was not unshadowed by isolation: %v", first.Outcomes["example.com/freshfixture/panicky.TestShadowed"])
	}

	second, err := RunWitnesses(context.Background(), tmp, noSeeding{})
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
	third, err := RunWitnesses(context.Background(), tmp, noSeeding{})
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
// apply to its race-selected counterpart — the published record carries
// the observation proof and no purity attribution — and an edit to a
// race-only helper must stale the test that reaches it. Each package has
// one selected test, so process isolation permits proof selection; the
// race I/O test's fixture read and harness failure channel are covered
// by its positive observation proof, so it serves like its sibling.
//
//gofresh:pure
func TestGoRunWitnessesSelectsRaceSources(t *testing.T) {
	// The witness store must never be the user's real one: an
	// un-overridden store poisons this package's own observation and
	// pollutes the host cache (t.Setenv forbids t.Parallel, which these
	// tests drop for hermeticity).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-race")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/racefixture")); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)

	first, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, first, "first run")
	if first.Fresh != 0 || first.Ran != 2 {
		t.Fatalf("first run: ran=%d fresh=%d, want both tests run", first.Ran, first.Fresh)
	}
	second, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, second, "second run")
	if second.Ran != 0 || second.Fresh != 2 {
		t.Fatalf("second run: ran=%d fresh=%d, want both tests served — the race I/O test under its observation proof", second.Ran, second.Fresh)
	}
	// The race-selected record serves under its own observation proof,
	// never the default-only declaration's purity assertion: a laundered
	// assertion would appear as purity attribution on the published
	// fingerprint.
	purityRecorded := false
	for _, rec := range witnesscache.Load(tmp) {
		if rec.Package != "example.com/racefixture/racepurity" || rec.Test != "TestRacePurity" {
			continue
		}
		purityRecorded = true
		if rec.Fingerprint.PurityAssertion != "" {
			t.Fatalf("race-selected record carries purity attribution %q — the default-only assertion rode across the build constraint", rec.Fingerprint.PurityAssertion)
		}
		if rec.Fingerprint.ObservationProof == (gofresh.ObservationProof{}) || !rec.Fingerprint.ObservationProof.Observable {
			t.Fatalf("race-selected record serves without an observable proof: %+v", rec.Fingerprint.ObservationProof)
		}
	}
	if !purityRecorded {
		t.Fatal("no published record for the race-selected I/O test")
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

	third, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, third, "race helper edit")
	if third.Ran != 1 || third.Fresh != 1 {
		t.Fatalf("race helper edit: ran=%d fresh=%d, want the closure test re-run on its race-only helper edit and the I/O test served", third.Ran, third.Fresh)
	}
	if third.Outcomes["example.com/racefixture/raceclosure.TestRaceClosure"] != verify.TestPassed {
		t.Fatalf("race-selected closure test did not pass after re-witnessing: %v", third.Outcomes)
	}

	fourth, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, fourth, "post-edit steady state")
	if fourth.Ran != 0 || fourth.Fresh != 2 {
		t.Fatalf("post-edit steady state: ran=%d fresh=%d, want both served — the recaptured closure test beside the proof-served I/O test", fourth.Ran, fourth.Fresh)
	}
}

// TestGoRunWitnessesConfigExcludedPathHonored pins the reviewed
// invocation-config exclusion end to end: a witness reading a session
// tool's bookkeeping file outside its package bracket is uncacheable —
// the read seals out-of-bracket — until the policy declares the
// directory excluded, after which the identity records nothing and the
// witness serves across content drift the exclusion asserts is no
// input.
func TestGoRunWitnessesConfigExcludedPathHonored(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	files := map[string]string{
		"go.mod":         "module example.com/exclfixture\n\ngo 1.26\n",
		".claude/marker": "session-state-1\n",
		"lib/lib.go":     "package lib\n\nfunc Two() int { return 2 }\n",
		"lib/lib_test.go": `package lib

import (
	"os"
	"testing"
)

func TestReadsSession(t *testing.T) {
	_, _ = os.ReadFile("../.claude/marker")
	if Two() != 2 {
		t.Fatal("wrong")
	}
}
`,
	}
	for _, tc := range []struct {
		name       string
		excluded   bool
		wantServed int
	}{
		{name: "declared exclusion serves across drift", excluded: true, wantServed: 1},
		{name: "undeclared stays uncacheable", excluded: false, wantServed: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			neutralAmbient(t)
			tmp := writeModule(t, files)
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{"./..."})
			cfg.SetRace(true)
			if tc.excluded {
				cfg.SetExcludedPaths([]string{".claude"})
			}
			p := &stipulatorv1.TestPolicy{}
			p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
			writePolicyRecord(t, tmp, p)
			first, err := RunWitnesses(context.Background(), tmp, noSeeding{})
			if err != nil {
				t.Fatal(err)
			}
			if first.Ran != 1 || first.Fresh != 0 {
				t.Fatalf("first run: ran=%d fresh=%d uncached=%d outcomes=%v, want 1 ran", first.Ran, first.Fresh, first.Uncached, first.Outcomes)
			}
			if err := os.WriteFile(filepath.Join(tmp, ".claude", "marker"), []byte("session-state-2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			second, err := RunWitnesses(context.Background(), tmp, noSeeding{})
			if err != nil {
				t.Fatal(err)
			}
			if second.Fresh != tc.wantServed || second.Ran != 1-tc.wantServed {
				t.Fatalf("second run: ran=%d fresh=%d, want fresh=%d", second.Ran, second.Fresh, tc.wantServed)
			}
			if !tc.excluded {
				if second.Uncached != 1 {
					t.Fatalf("second run: uncached=%d, want the out-of-bracket read permanently uncacheable", second.Uncached)
				}
				return
			}
			// Additions serve existing evidence unchanged: the new
			// entry's identities are in no record's licensing set.
			widened := &stipulatorv1.GoInvocationConfig{}
			widened.SetPackages([]string{"./..."})
			widened.SetRace(true)
			widened.SetExcludedPaths([]string{".claude", "unrelated"})
			pw := &stipulatorv1.TestPolicy{}
			pw.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", widened)})
			writePolicyRecord(t, tmp, pw)
			third, err := RunWitnesses(context.Background(), tmp, noSeeding{})
			if err != nil {
				t.Fatal(err)
			}
			if third.Fresh != 1 || third.Ran != 0 {
				t.Fatalf("widened run: ran=%d fresh=%d, want served", third.Ran, third.Fresh)
			}
			// Withdrawal re-runs the witnesses the entry licensed: the
			// record's observation proves nothing about the elided
			// surface, and the current policy no longer asserts it.
			bare := &stipulatorv1.GoInvocationConfig{}
			bare.SetPackages([]string{"./..."})
			bare.SetRace(true)
			pb := &stipulatorv1.TestPolicy{}
			pb.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", bare)})
			writePolicyRecord(t, tmp, pb)
			if err := os.WriteFile(filepath.Join(tmp, ".claude", "marker"), []byte("session-state-3\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			fourth, err := RunWitnesses(context.Background(), tmp, noSeeding{})
			if err != nil {
				t.Fatal(err)
			}
			if fourth.Ran != 1 || fourth.Fresh != 0 {
				t.Fatalf("withdrawn run: ran=%d fresh=%d, want re-executed", fourth.Ran, fourth.Fresh)
			}
		})
	}
}

// TestRoundCandidatesAdvancesPastGateRefusal pins the variant-walk
// contract under the exclusion gate: a gate-refused variant is round
// progress, never the walk's end, so a poisoned early variant can
// never permanently mask a serveable later one.
func TestRoundCandidatesAdvancesPastGateRefusal(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	s := gofresh.Subject{Package: "p", Symbol: "T"}
	cached := map[string][]witnesscache.Record{"p.T": {
		{ObservationExclusions: []string{"withdrawn"}},
		{},
	}}
	refused := 0
	fps, advanced := roundCandidates([]gofresh.Subject{s}, cached, map[gofresh.Subject]bool{}, 0, nil, nil, func(gofresh.Subject, string) { refused++ })
	if !advanced || len(fps) != 0 || refused != 1 {
		t.Fatalf("round 0: advanced=%v fps=%d refused=%d, want advancement past the gate-refused variant", advanced, len(fps), refused)
	}
	if fps, advanced = roundCandidates([]gofresh.Subject{s}, cached, map[gofresh.Subject]bool{}, 1, nil, nil, func(gofresh.Subject, string) { refused++ }); !advanced || len(fps) != 1 {
		t.Fatalf("round 1: advanced=%v fps=%d, want the later variant checked", advanced, len(fps))
	}
	if _, advanced = roundCandidates([]gofresh.Subject{s}, cached, map[gofresh.Subject]bool{}, 2, nil, nil, func(gofresh.Subject, string) { refused++ }); advanced {
		t.Fatal("round past the last variant still advances")
	}
}

// TestGoRunWitnessesWithdrawnVariantNeverMasksServeable is the
// integration net over the same contract: after a withdrawal republish,
// two variants coexist — the gate-refused one must not end the walk
// before the recorded one proves equivalent, whatever the load order.
func TestGoRunWitnessesWithdrawnVariantNeverMasksServeable(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/maskfixture\n\ngo 1.26\n",
		"lib/cache/state": "warm\n",
		"lib/lib.go":      "package lib\n\nfunc Two() int { return 2 }\n",
		"lib/lib_test.go": `package lib

import (
	"os"
	"testing"
)

func TestReadsCache(t *testing.T) {
	_, _ = os.ReadFile("cache/state")
	if Two() != 2 {
		t.Fatal("wrong")
	}
}
`,
	})
	policy := func(excluded []string) {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(true)
		if len(excluded) > 0 {
			cfg.SetExcludedPaths(excluded)
		}
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
		writePolicyRecord(t, tmp, p)
	}
	policy([]string{"lib/cache"})
	first, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Ran != 1 {
		t.Fatalf("first run: ran=%d, want 1", first.Ran)
	}
	policy(nil)
	second, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Ran != 1 || second.Fresh != 0 {
		t.Fatalf("withdrawal run: ran=%d fresh=%d, want re-executed", second.Ran, second.Fresh)
	}
	third, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if third.Fresh != 1 || third.Ran != 0 {
		t.Fatalf("post-republish run: ran=%d fresh=%d, want the recorded variant served", third.Ran, third.Fresh)
	}
}

// TestGoRunWitnessesCompletedGroupSurvivesLaterInvocationFailure pins
// completed-group durability: records install the moment their last
// covering invocation completes, so a later invocation's failure — the
// run erroring out mid-execution — keeps every record already produced
// (REQ-evidence-witness-cache-format).
//
//gofresh:pure
func TestGoRunWitnessesCompletedGroupSurvivesLaterInvocationFailure(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-format")
	if testing.Short() {
		t.Skip("executes selective runs over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":            "module example.com/durable\n\ngo 1.26\n",
		"good/good.go":      "package good\n\nfunc Fine() int { return 1 }\n",
		"good/good_test.go": "package good\n\nimport \"testing\"\n\nfunc TestFine(t *testing.T) {\n\tif Fine() != 1 {\n\t\tt.Fatal(\"fine\")\n\t}\n}\n",
		"slow/slow.go":      "package slow\n\nfunc Steady() int { return 1 }\n",
		"slow/slow_test.go": "package slow\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSteady(t *testing.T) {\n\ttime.Sleep(90 * time.Second)\n\tif Steady() != 1 {\n\t\tt.Fatal(\"steady\")\n\t}\n}\n",
	})
	goodCfg := &stipulatorv1.GoInvocationConfig{}
	goodCfg.SetPackages([]string{"./good/..."})
	goodCfg.SetRace(true)
	slowCfg := &stipulatorv1.GoInvocationConfig{}
	slowCfg.SetPackages([]string{"./slow/..."})
	slowCfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("good", goodCfg),
		goInvocation("slow", slowCfg),
	})
	writePolicyRecord(t, tmp, p)

	hasGood := func() bool {
		for _, rec := range witnesscache.Load(tmp) {
			if rec.Package == "example.com/durable/good" && rec.Test == "TestFine" {
				return true
			}
		}
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunWitnesses(ctx, tmp, noSeeding{})
		done <- err
	}()
	// The behavioral discriminator is the done-case below (a run that
	// ends before the completed group is observable fails); this
	// deadline only bounds the test's own runtime and must absorb
	// nested race-build latency on a loaded machine - 120s was exceeded
	// by a healthy run under a concurrent whole-tree check.
	deadline := time.Now().Add(8 * time.Minute)
	for !hasGood() {
		select {
		case err := <-done:
			t.Fatalf("run ended before the completed group installed: %v (store: %+v)", err, witnesscache.Load(tmp))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("completed group never installed mid-run (store: %+v)", witnesscache.Load(tmp))
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The discriminating assertion: at the moment the completed group's
	// record is first observable, the slow group's must be absent - an
	// end-of-run batch installs both together, incremental publication
	// installs each at its own group's completion.
	hasSlow := func() bool {
		for _, rec := range witnesscache.Load(tmp) {
			if rec.Package == "example.com/durable/slow" {
				return true
			}
		}
		return false
	}
	if hasSlow() {
		t.Fatal("slow group's record present alongside the completed group's - end-of-run batching")
	}
	cancel()
	// Cancellation mid-execution may surface as an errored run or as a
	// completed run whose interrupted process carries a red disposition
	// (the run survives any single process outcome); the durability
	// claim is the same either way - the completed group's record,
	// installed before the interruption, stands, and the cancelled
	// slow group never publishes.
	<-done
	if !hasGood() {
		t.Fatal("completed group's record lost to the cancellation")
	}
	if hasSlow() {
		t.Fatal("cancelled slow group published a record")
	}
}

// TestServedRecordRevalidatesAfterTheRunsExecutions pins the served half
// of the package unit (REQ-check-witness-selection, REQ-policy-cancellation):
// a package's served records are revalidated after every execution of the
// run, never at the package's own completion, so a sibling package that
// rewrites a served reader's input after the reader's package has finished
// still disproves the serve — the reader re-executes in the run's drift
// retry and its record re-derives; nothing the run reports is served from
// a record the tree it finished on disproves. The writer's rewrite is
// gated on the reader's persisted note, so the interleaving is the test's.
func TestServedRecordRevalidatesAfterTheRunsExecutions(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection", "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes race-instrumented selective runs over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":           "module example.com/sibserve\n\ngo 1.26\n",
		"reader/data.txt":  "v1\n",
		"reader/touch.txt": "fail\n",
		// TestReads serves on the second run; TestTouch is red on the
		// first run and green on the second, so the reader package still
		// executes something and publishes at its own completion.
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

//gofresh:pure
func TestTouch(t *testing.T) {
	b, err := os.ReadFile("touch.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "fail\n" {
		t.Fatal("red")
	}
}
`,
		"writer/trigger.txt": "no\n",
		"writer/writer_test.go": `package writer

import (
	"os"
	"strings"
	"testing"
	"time"
)

//gofresh:pure
func TestWritesOnce(t *testing.T) {
	raw, err := os.ReadFile("trigger.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != "yes" {
		return
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat("go.ahead"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the reader's package never persisted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := os.WriteFile("../reader/data.txt", []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	writeRacePolicy(t, tmp)
	if _, err := RunWitnesses(context.Background(), tmp, noSeeding{}); err != nil {
		t.Fatal(err)
	}
	cold := cacheRecord(t, witnesscache.Load(tmp), "example.com/sibserve/reader", "TestReads")
	if cold == nil {
		t.Fatal("no cold record for the reader")
	}
	if err := os.WriteFile(filepath.Join(tmp, "writer", "trigger.txt"), []byte("yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "reader", "touch.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) {
		if strings.HasPrefix(e.GetNote(), "persisted: ") && strings.Contains(e.GetNote(), "example.com/sibserve/reader (") {
			if err := os.WriteFile(filepath.Join(tmp, "writer", "go.ahead"), nil, 0o644); err != nil {
				t.Error(err)
			}
		}
	}, progress.WithInterval(time.Hour))
	run, err := RunWitnesses(progress.NewContext(context.Background(), rep), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "reader", "data.txt")); string(b) != "v2\n" {
		t.Fatalf("the writer never rewrote the served reader's input: %q", b)
	}
	if run.Fresh != 0 {
		t.Fatalf("the run served %d witness(es) the tree it finished on disproves", run.Fresh)
	}
	if got := run.Outcomes["example.com/sibserve/reader.TestReads"]; got != verify.TestPassed {
		t.Fatalf("the reader's drift retry lost its outcome: %v", got)
	}
	rederived := cacheRecord(t, witnesscache.Load(tmp), "example.com/sibserve/reader", "TestReads")
	if rederived == nil || rederived.Fingerprint.RuntimeDigest == cold.Fingerprint.RuntimeDigest {
		t.Fatalf("the reader's record was not re-derived against the rewritten input: cold %v, now %+v", cold.Fingerprint.RuntimeDigest, rederived)
	}
}

// TestExecutedRecordDropsWhenItsInputMovesBeforeItsPublish pins the
// executed half's drift window on the selective form
// (REQ-evidence-witness-freshness): an input a package's test read that
// moves between the package's process and its publish is a mid-run drift
// of the record's inputs — the record is dropped, counted uncacheable,
// and the next run re-derives it; the executed outcome stands.
func TestExecutedRecordDropsWhenItsInputMovesBeforeItsPublish(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":          "module example.com/prepublish\n\ngo 1.26\n",
		"reader/data.txt": "before",
		"reader/reader_test.go": `package reader

import (
	"os"
	"testing"
)

func TestReads(t *testing.T) {
	if _, err := os.ReadFile("data.txt"); err != nil {
		t.Fatal(err)
	}
}
`,
	})
	writeRacePolicy(t, tmp)
	saved := beforePackagePublishForTest
	t.Cleanup(func() { beforePackagePublishForTest = saved })
	var moved int
	beforePackagePublishForTest = func(pkg string) {
		if pkg == "example.com/prepublish/reader" {
			moved++
			if err := os.WriteFile(filepath.Join(tmp, "reader", "data.txt"), []byte("after"), 0o644); err != nil {
				t.Error(err)
			}
		}
	}
	run, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("the seam ran %d times, want once before the reader's publish", moved)
	}
	if got := run.Outcomes["example.com/prepublish/reader.TestReads"]; got != verify.TestPassed {
		t.Fatalf("the executed outcome did not stand: %v", got)
	}
	if rec := cacheRecord(t, witnesscache.Load(tmp), "example.com/prepublish/reader", "TestReads"); rec != nil {
		t.Fatalf("a record published although its input moved before its publish: %+v", *rec)
	}
	if run.Uncached == 0 {
		t.Fatal("the dropped record was not counted uncacheable")
	}
}

// TestServedDiscardsWhenTheTreeMovesInsideTheCloseInterval pins the
// deferred-close contract on the served half (REQ-check-witness-selection,
// REQ-evidence-witness-freshness): a served record's re-check verdict is
// provisional until its package's sibling validates, so a source move
// landing between the re-check and that closing validation refuses the
// close — every serve of the package is discarded and re-executes in the
// drift retry — never passes as a serve the tree disproves. (A runtime
// input moving there is not the case: the reader asserts purity, and a
// purity-asserted record's inputs are the author's word, by design.)
func TestServedDiscardsWhenTheTreeMovesInsideTheCloseInterval(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection", "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes race-instrumented selective runs over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":           "module example.com/closewin\n\ngo 1.26\n",
		"reader/data.txt":  "v1\n",
		"reader/touch.txt": "fail\n",
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

//gofresh:pure
func TestTouch(t *testing.T) {
	b, err := os.ReadFile("touch.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "fail\n" {
		t.Fatal("red")
	}
}
`,
	})
	writeRacePolicy(t, tmp)
	if _, err := RunWitnesses(context.Background(), tmp, noSeeding{}); err != nil {
		t.Fatal(err)
	}
	cold := cacheRecord(t, witnesscache.Load(tmp), "example.com/closewin/reader", "TestReads")
	if cold == nil {
		t.Fatal("no cold record for the reader")
	}
	// TestTouch goes green, so the package executes and TestReads serves.
	if err := os.WriteFile(filepath.Join(tmp, "reader", "touch.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := afterServedCheckForTest
	t.Cleanup(func() { afterServedCheckForTest = saved })
	var moved int
	afterServedCheckForTest = func(pkg string) {
		if pkg == "example.com/closewin/reader" {
			moved++
			// A source move inside the interval: TestTouch's body changes,
			// so the reader package's closure no longer matches the view.
			src := "package reader\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n//gofresh:pure\nfunc TestReads(t *testing.T) {\n\tif _, err := os.ReadFile(\"data.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n\n//gofresh:pure\nfunc TestTouch(t *testing.T) {\n\tif _, err := os.ReadFile(\"touch.txt\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n}\n"
			if err := os.WriteFile(filepath.Join(tmp, "reader", "reader_test.go"), []byte(src), 0o644); err != nil {
				t.Error(err)
			}
		}
	}
	run, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("the seam ran %d times, want once inside the reader's close interval", moved)
	}
	if run.Fresh != 0 {
		t.Fatalf("the run served %d witness(es) through a close its tree refused", run.Fresh)
	}
	if got := run.Outcomes["example.com/closewin/reader.TestReads"]; got != verify.TestPassed {
		t.Fatalf("the discarded serve's retry lost its outcome: %v", got)
	}
}
