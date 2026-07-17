package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

type pkgPlan struct {
	pkg   string
	stale []string
	fps   map[string]gofresh.Fingerprint
}

// RunTestsFresh is the freshness-aware witness run
// (REQ-evidence-witness-freshness): each expected top-level test whose
// cached fingerprint checks valid against the current tree serves its
// outcomes and registrations from the cache — verification by proven
// equivalence — and only the rest run, per package, with the run's testlog
// captured so the new fingerprints carry the package's observed
// runtime-input manifest. Any fault on the freshness path degrades to the
// full run: the cache saves work, never blocks witnessing.
func RunTestsFresh(dir string) (*verify.TestRun, error) {
	return RunTestsFreshContext(context.Background(), dir)
}

// RunTestsFreshContext performs a freshness-aware witness run bound to ctx.
func RunTestsFreshContext(ctx context.Context, dir string) (*verify.TestRun, error) {
	tr, err := runTestsFresh(ctx, dir)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		fmt.Fprintf(os.Stderr, "witness cache unavailable (%v); running the full suite\n", err)
		full, ferr := RunTestsContext(ctx, dir)
		if ferr != nil {
			return nil, ferr
		}
		full.Degraded = err.Error()
		return full, nil
	}
	return tr, nil
}

func runTestsFresh(ctx context.Context, dir string) (*verify.TestRun, error) {
	backend, err := NewContext(ctx, dir)
	if err != nil {
		return nil, err
	}
	expected := backend.RunnableTests()
	if len(expected) == 0 {
		return nil, fmt.Errorf("no runnable tests enumerated")
	}
	pkgs := make([]string, 0, len(expected))
	for p := range expected {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	buildFlags := []string{"-race"}
	// The engine analyzes under the same GOWORK pinning as every witness
	// invocation (goworkEnv): an ambient workspace pointing elsewhere —
	// the outer harness's, when this run is itself a witness — would
	// resolve the module under the wrong workspace and break go list.
	engine, err := gofresh.New(
		gofresh.WithDir(dir),
		gofresh.WithBuildFlags(buildFlags...),
		gofresh.WithEnv(goworkEnv(dir)...),
	)
	if err != nil {
		return nil, err
	}
	var subjects []gofresh.Subject
	for _, pkg := range pkgs {
		for _, test := range expected[pkg] {
			subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: test})
		}
	}
	view, err := engine.NewView(ctx, subjects, dir)
	if err != nil {
		return nil, err
	}

	cached := map[string]witnesscache.Record{}
	for _, rec := range witnesscache.Load(dir) {
		cached[rec.Key()] = rec
	}

	env := goworkEnv(dir)
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true}
	var next []witnesscache.Record
	served := map[string]bool{}
	recorded := map[gofresh.Subject]gofresh.Fingerprint{}
	for _, pkg := range pkgs {
		for _, test := range expected[pkg] {
			if rec, ok := cached[pkg+"."+test]; ok {
				recorded[gofresh.Subject{Package: pkg, Symbol: test}] = rec.Fingerprint.ToGofresh()
			}
		}
	}
	verdicts, err := checkFingerprints(ctx, view, recorded)
	if err != nil {
		return nil, err
	}

	// One engine pass over every package — check what serves, and capture
	// fingerprints for what must run — before any test executes. Capturing
	// before the run is a correctness constraint: the closure hash must be
	// of the tree that compiles the binary; captured after, an edit made
	// while the tests run would pin pre-edit outcomes under a post-edit
	// hash — a Valid verdict for evidence the current tree never produced.
	// Captured before, the same interleaving records a hash the edited tree
	// no longer matches: Stale, the safe direction.
	var plans []pkgPlan
	var observationCandidates []gofresh.Subject
	for _, pkg := range pkgs {
		var stale []string
		for _, test := range expected[pkg] {
			subject := gofresh.Subject{Package: pkg, Symbol: test}
			rec, ok := cached[pkg+"."+test]
			if !ok {
				stale = append(stale, test)
				continue
			}
			if verdicts[subject].Status != gofresh.Valid {
				stale = append(stale, test)
				continue
			}
			// Proven equivalent: serve the record.
			for key, out := range rec.Outcomes {
				tr.Outcomes[key] = outcomeFromString(out)
			}
			tr.Registrations = append(tr.Registrations, rec.Regs...)
			tr.Fresh++
			next = append(next, rec)
			served[rec.Key()] = true
		}
		if len(stale) == 0 {
			continue
		}
		fps := map[string]gofresh.Fingerprint{}
		for _, test := range stale {
			if fp, err := view.Capture(gofresh.Subject{Package: pkg, Symbol: test}); err == nil {
				fps[test] = fp
			}
		}
		// A per-subject completeness assertion cannot be derived from a
		// process shared with sibling tests: a sibling can mutate process
		// state the subject observes without appearing in its closure.
		if len(stale) == 1 && fps[stale[0]].PurityAssertion == "" {
			observationCandidates = append(observationCandidates, gofresh.Subject{Package: pkg, Symbol: stale[0]})
		}
		plans = append(plans, pkgPlan{pkg: pkg, stale: stale, fps: fps})
	}
	observed, observedFPs := observedView(ctx, engine, observationCandidates, dir)

	// Release transient package-loading memory before spawning race-instrumented
	// builds. The bounded maximal view remains alive because producer validation
	// must re-observe it after execution.
	debug.FreeOSMemory()

	// Packages execute concurrently under a small bound: race-
	// instrumented builds dominate the wall clock and are independent
	// per package. Each worker folds into a private shard; shards
	// merge in plan order, so outcomes, registrations, and published
	// records land deterministically regardless of completion order.
	type shard struct {
		tr  *verify.TestRun
		run *selectedRun
		err error
	}
	shards := make([]shard, len(plans))
	sem := make(chan struct{}, witnessParallelism())
	var wg sync.WaitGroup
	for i, plan := range plans {
		// Acquire in dispatch order: goroutines SPAWN in plan order,
		// and under a bound of 1 each completes before the next spawns
		// (the deterministic mode the drift fixtures pin). Above 1,
		// execution and completion order race by design.
		if !acquireWitnessSlot(ctx, sem, &wg) {
			wg.Wait()
			return nil, ctx.Err()
		}
		go func(i int, plan pkgPlan) {
			defer wg.Done()
			defer func() { <-sem }()
			private := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true}
			ran, err := runSelected(ctx, dir, env, plan.pkg, plan.stale, private)
			if err != nil {
				shards[i] = shard{err: err}
				return
			}
			for _, test := range plan.stale {
				if _, ok := ran.outcomes[plan.pkg+"."+test]; ok {
					private.Ran++
				}
			}
			shards[i] = shard{tr: private, run: ran}
		}(i, plan)
	}
	wg.Wait()
	for _, sh := range shards {
		if sh.err != nil {
			return nil, sh.err
		}
		if sh.tr == nil {
			continue
		}
		for key, out := range sh.tr.Outcomes {
			tr.Outcomes[key] = out
		}
		for key, failure := range sh.tr.Failures {
			if tr.Failures == nil {
				tr.Failures = map[string]string{}
			}
			tr.Failures[key] = failure
		}
		tr.Registrations = append(tr.Registrations, sh.tr.Registrations...)
		tr.Ran += sh.tr.Ran
	}
	if err := view.Validate(ctx); err != nil {
		return nil, err
	}
	validatedObserved := map[gofresh.Subject]bool{}
	if observed != nil && len(observedFPs) == len(observationCandidates) {
		complete := true
		attached := make(map[gofresh.Subject]gofresh.Fingerprint, len(observationCandidates))
		attachedValid := make(map[gofresh.Subject]bool, len(observationCandidates))
		for i, plan := range plans {
			for _, test := range plan.stale {
				subject := gofresh.Subject{Package: plan.pkg, Symbol: test}
				observedFP, selected := observedFPs[subject]
				if !selected {
					continue
				}
				capture, ok := shards[i].run.capture[test]
				if !ok {
					complete = false
					break
				}
				fp, err := observed.AttachObservation(subject, observedFP, capture.observation)
				if err != nil {
					return nil, err
				}
				attached[subject] = fp
				state, err := runtimeinput.CompletedState(capture.observation)
				if err != nil {
					return nil, err
				}
				attachedValid[subject] = validatedObservation(fp, state)
			}
			if !complete {
				break
			}
		}
		if complete {
			if err := observed.ValidateObserved(ctx); err != nil {
				return nil, err
			}
			for subject := range attached {
				validatedObserved[subject] = attachedValid[subject]
			}
			for i := range plans {
				for test := range plans[i].fps {
					subject := gofresh.Subject{Package: plans[i].pkg, Symbol: test}
					if fp, ok := attached[subject]; ok {
						plans[i].fps[test] = fp
					}
				}
			}
		}
	}
	for i, plan := range plans {
		next = append(next, fingerprintRan(plan, shards[i].run)...)
	}
	if len(next) != 0 && len(plans) != 0 {
		final := make(map[gofresh.Subject]gofresh.Fingerprint, len(next))
		for _, rec := range next {
			final[gofresh.Subject{Package: rec.Package, Symbol: rec.Test}] = rec.Fingerprint.ToGofresh()
		}
		unvalidated := make(map[gofresh.Subject]gofresh.Fingerprint, len(final))
		verdicts := make(map[gofresh.Subject]gofresh.Verdict, len(final))
		for subject, fingerprint := range final {
			if validatedObserved[subject] {
				verdicts[subject] = gofresh.Verdict{Status: gofresh.Valid}
			} else {
				unvalidated[subject] = fingerprint
			}
		}
		checked, err := checkFingerprints(ctx, view, unvalidated)
		if err != nil {
			return nil, err
		}
		maps.Copy(verdicts, checked)
		publish := next[:0]
		for _, rec := range next {
			subject := gofresh.Subject{Package: rec.Package, Symbol: rec.Test}
			switch verdicts[subject].Status {
			case gofresh.Valid:
				publish = append(publish, rec)
			case gofresh.Unverifiable:
				if !served[rec.Key()] {
					continue
				}
				fallthrough
			default:
				v := verdicts[subject]
				return nil, fmt.Errorf("witness %s.%s moved during execution (%s: %s)", subject.Package, subject.Symbol, v.Status, v.Reason)
			}
		}
		next = publish
	}
	sortRegs(tr)
	// Uncached is structural — executed tests minus fresh records that
	// survived to publication — so every drop path counts: aborted
	// invocations, missing manifest captures, failed pre-run captures,
	// and unverifiable post-run verdicts alike. A silently shrinking
	// cache must read as a number, never as "covered".
	freshPublished := 0
	for _, rec := range next {
		if !served[rec.Key()] {
			freshPublished++
		}
	}
	if tr.Ran > freshPublished {
		tr.Uncached = tr.Ran - freshPublished
	}
	if err := witnesscache.EnsureIgnored(dir); err == nil {
		_ = witnesscache.Save(dir, next)
	}
	return tr, nil
}

// checkFingerprints checks a recording set with one shared drift bracket pair,
// runtime window, and precise analysis per policy class: observed recordings
// batch through the observed policy, the rest through the ordinary hierarchical
// policy. Per-record checking multiplied full workspace observations by the
// record count.
func checkFingerprints(ctx context.Context, view *gofresh.View, recorded map[gofresh.Subject]gofresh.Fingerprint) (map[gofresh.Subject]gofresh.Verdict, error) {
	observed := make(map[gofresh.Subject]gofresh.Fingerprint, len(recorded))
	plain := make(map[gofresh.Subject]gofresh.Fingerprint, len(recorded))
	for subject, fingerprint := range recorded {
		if fingerprint.ObservationAssertion != "" || fingerprint.ObservationProof != (gofresh.ObservationProof{}) {
			observed[subject] = fingerprint
		} else {
			plain[subject] = fingerprint
		}
	}
	verdicts := make(map[gofresh.Subject]gofresh.Verdict, len(recorded))
	if len(observed) != 0 {
		batch, err := view.CheckObservedBatch(ctx, observed)
		if err != nil {
			return nil, err
		}
		maps.Copy(verdicts, batch)
	}
	if len(plain) != 0 {
		batch, err := view.CheckRefinedBatch(ctx, plain)
		if err != nil {
			return nil, err
		}
		maps.Copy(verdicts, batch)
	}
	return verdicts, nil
}

func validatedObservation(fingerprint gofresh.Fingerprint, state runtimeinput.State) bool {
	return fingerprint.ObservationProof.Observable && !state.Unverifiable
}

func acquireWitnessSlot(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup) bool {
	select {
	case sem <- struct{}{}:
		if ctx.Err() != nil {
			<-sem
			return false
		}
		wg.Add(1)
		return true
	case <-ctx.Done():
		return false
	}
}

// witnessParallelism bounds concurrent package executions: race-
// instrumented builds are memory-heavy, so the bound stays small and
// never exceeds the machine. STIPULATOR_WITNESS_PARALLEL overrides
// (a positive integer; 1 serializes — the deterministic-interleaving
// mode the drift fixtures pin).
func witnessParallelism() int {
	if raw := os.Getenv("STIPULATOR_WITNESS_PARALLEL"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if max := runtime.GOMAXPROCS(0); n > max {
				return max
			}
			return n
		}
	}
	n := runtime.GOMAXPROCS(0) / 2
	if n < 1 {
		return 1
	}
	if n > 4 {
		return 4
	}
	return n
}

// selectedRun is one package's selective execution: the parsed outcomes and
// registrations scoped to it, and per top-level test the runtime-input
// manifest of the invocation that produced its outcome — present only when
// that invocation was clean, so a missing entry means "do not cache".
type selectedRun struct {
	outcomes map[string]string
	regs     map[string][]verify.Registration
	capture  map[string]manifestCapture
}

// manifestCapture is one clean invocation's runtime-input evidence.
type manifestCapture struct {
	observation runtimeinput.Observation
}

// runSelected executes exactly the named tests of one package, folding
// outcomes and registrations into tr. A package abort — a panic or runtime
// fatal kills the binary before the remaining selected tests run, and
// before the testing runtime flushes its testlog — leaves that invocation
// without a trustworthy manifest, so the incomplete remainder re-runs in a
// fresh invocation until everything selected has an outcome or an
// invocation makes no progress. Caching eligibility is decided per
// invocation, never per package run: an absent manifest is an assertion in
// gofresh, so evidence from an aborted invocation must not be cached at all.
func runSelected(ctx context.Context, dir string, env []string, pkg string, tests []string, tr *verify.TestRun) (*selectedRun, error) {
	run := &selectedRun{outcomes: map[string]string{}, regs: map[string][]verify.Registration{}, capture: map[string]manifestCapture{}}
	remaining := tests
	for len(remaining) > 0 {
		completed, err := runOnce(ctx, dir, env, pkg, remaining, tr, run)
		if err != nil {
			return nil, err
		}
		if len(completed) == 0 {
			// No progress: whatever is left never reaches an outcome this
			// run. It stays uncached and simply runs again next time.
			break
		}
		var left []string
		for _, t := range remaining {
			if !completed[t] {
				left = append(left, t)
			}
		}
		remaining = left
	}
	return run, nil
}

// runOnce is a single go test invocation over the named tests, with its own
// testlog. It reports which of them reached a top-level outcome. The
// invocation's manifest is recorded for those tests only when the invocation
// is clean: every named test completed, any non-zero exit is explained by a
// test-level failure, no abort marker appeared in the output, and the
// testlog parsed whole. Anything else means the testlog flush cannot be
// trusted (it runs after the last test, so an abort loses or truncates it),
// and a lost read must fail closed as "do not cache", never masquerade as
// gofresh's "no runtime inputs observed" assertion — the failure direction
// is a spurious re-run, never a spurious reuse.
func runOnce(ctx context.Context, dir string, env []string, pkg string, tests []string, tr *verify.TestRun, run *selectedRun) (map[string]bool, error) {
	logf, err := os.CreateTemp("", "stipulator-testlog-*.txt")
	if err != nil {
		return nil, err
	}
	logPath := logf.Name()
	logf.Close()
	defer os.Remove(logPath)

	pattern := "^(" + strings.Join(tests, "|") + ")$"
	// An explicit timeout: the default 10m is per test binary and nested
	// freshness witnesses exceed 30m under -race with testlog observation; a
	// kill mid-binary masquerades as a test failure and retries the remainder.
	args := []string{"test", "-json", "-race", "-timeout=60m", "-run", pattern, pkg, "-args", "-test.testlogfile=" + logPath}
	cmd := commandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	type event struct {
		Action, Package, Test, Output string
	}
	completed := map[string]bool{}
	output := map[string][]string{}
	sawFail := false
	sawAbort := false
	dec := json.NewDecoder(&stdout)
	events := 0
	for dec.More() {
		var e event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("parsing go test -json stream: %w", err)
		}
		events++
		if e.Action == "output" && isAbortOutput(e.Output) {
			sawAbort = true
		}
		if e.Test == "" {
			continue
		}
		key := e.Package + "." + e.Test
		top := !strings.Contains(e.Test, "/")
		switch e.Action {
		case "pass":
			tr.Outcomes[key] = verify.TestPassed
			run.outcomes[key] = "passed"
			if top {
				completed[e.Test] = true
				delete(output, e.Test)
			}
		case "fail":
			tr.Outcomes[key] = verify.TestFailed
			run.outcomes[key] = "failed"
			if top {
				completed[e.Test] = true
				sawFail = true
				if tr.Failures == nil {
					tr.Failures = map[string]string{}
				}
				tr.Failures[key] = strings.Join(output[e.Test], "")
			}
		case "skip":
			tr.Outcomes[key] = verify.TestSkipped
			run.outcomes[key] = "skipped"
			if top {
				completed[e.Test] = true
				delete(output, e.Test)
			}
		case "output":
			o := output[topLevel(e.Test)]
			o = append(o, e.Output)
			if len(o) > failureOutputLines {
				o = o[len(o)-failureOutputLines:]
			}
			output[topLevel(e.Test)] = o
			for _, m := range coversRe.FindAllStringSubmatch(e.Output, -1) {
				reg := verify.Registration{Package: e.Package, Test: e.Test, Requirement: m[1]}
				tr.Registrations = append(tr.Registrations, reg)
				run.regs[topLevel(e.Test)] = append(run.regs[topLevel(e.Test)], reg)
			}
		}
	}
	if events == 0 && runErr != nil {
		return nil, fmt.Errorf("go test -json for %s produced no events: %v: %s", pkg, runErr, stderr.String())
	}

	clean := len(completed) == len(tests) && !sawAbort && (runErr == nil || sawFail)
	if !clean {
		return completed, nil
	}
	if log, err := os.ReadFile(logPath); err == nil {
		if pkgDir, ok := packageDir(ctx, dir, env, pkg); ok {
			// VCS bookkeeping and the root listing are never witness
			// inputs: their digests move under unrelated tooling (a
			// shell prompt's git status), which is exactly the
			// observation-coherence noise REQ-inputs-exclusions exists
			// to silence. The corpus files themselves stay recorded
			// individually, so real input changes still stale.
			if observation, err := runtimeinput.FromTestLogEnv(log, dir, pkgDir, env,
				runtimeinput.WithCompletedProcess(pkg),
				runtimeinput.WithExcludedPaths(".", ".git")); err == nil {
				for t := range completed {
					run.capture[t] = manifestCapture{observation: observation}
				}
			}
		}
	}
	return completed, nil
}

// failureOutputLines bounds the per-test output tail kept for a failure —
// enough for the assertion and its context, never the whole stream.
const failureOutputLines = 60

// isAbortOutput recognizes the output of a dying test binary. A test that
// legitimately prints these words costs a spurious no-cache for its whole
// invocation — every test that ran alongside it re-runs next time — and
// nothing more.
func isAbortOutput(s string) bool {
	return strings.Contains(s, "panic: ") || strings.Contains(s, "fatal error: ")
}

// observedView selects observation-completeness proof for every unasserted
// subject in one batch. Failure leaves the ordinary maximal captures in force.
func observedView(ctx context.Context, engine *gofresh.Engine, subjects []gofresh.Subject, dir string) (*gofresh.View, map[gofresh.Subject]gofresh.Fingerprint) {
	if len(subjects) == 0 {
		return nil, nil
	}
	view, err := engine.NewView(ctx, subjects, dir)
	if err != nil {
		return nil, nil
	}
	captured, err := view.CaptureObservedBatch(ctx)
	if err != nil {
		return nil, nil
	}
	return view, captured
}

// fingerprintRan assembles the cache records for tests that just ran under
// a clean invocation, from the fingerprints captured before the run. A test
// without a recorded manifest capture (its invocation aborted, or the
// testlog could not be read whole), without an outcome, or whose pre-run
// fingerprint capture failed stays uncached — it simply runs again next
// time.
func fingerprintRan(plan pkgPlan, run *selectedRun) []witnesscache.Record {
	fps := plan.fps
	var out []witnesscache.Record
	for _, test := range plan.stale {
		cap, ok := run.capture[test]
		if !ok {
			continue
		}
		fp, ok := fps[test]
		if !ok {
			continue
		}
		prefix := plan.pkg + "." + test
		outcomes := map[string]string{}
		for key, o := range run.outcomes {
			if key == prefix || strings.HasPrefix(key, prefix+"/") {
				outcomes[key] = o
			}
		}
		if len(outcomes) == 0 {
			continue
		}
		if fp.ObservationAssertion == "" {
			state, err := runtimeinput.CompletedState(cap.observation)
			if err != nil {
				continue
			}
			fp.RuntimeInputs, fp.RuntimeDigest = state.Manifest, state.Digest
		}
		out = append(out, witnesscache.Record{
			Package:     plan.pkg,
			Test:        test,
			Fingerprint: witnesscache.FromGofresh(fp),
			Outcomes:    outcomes,
			Regs:        run.regs[test],
		})
	}
	return out
}

// topLevel is the top-level test name of a possibly-subtest path.
func topLevel(test string) string {
	if i := strings.Index(test, "/"); i >= 0 {
		return test[:i]
	}
	return test
}

// packageDir resolves a package's directory for testlog path resolution.
func packageDir(ctx context.Context, dir string, env []string, pkg string) (string, bool) {
	cmd := commandContext(ctx, "go", "list", "-f", "{{.Dir}}", pkg)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// outcomeFromString maps a cached outcome back to the verify enum; an
// unknown word reads as not-run, which the correlator treats as
// unwitnessed — the conservative direction.
func outcomeFromString(s string) verify.TestOutcome {
	switch s {
	case "passed":
		return verify.TestPassed
	case "failed":
		return verify.TestFailed
	case "skipped":
		return verify.TestSkipped
	}
	return verify.TestNotRun
}
