package golang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// gofresh here is a library dependency by gofresh's own design
// (caller-owned fingerprints, REQ-fresh-fingerprint-data); the
// documents-only seam stipulator keeps toward the mutation engine is
// specific to that tool pair and does not apply.
//
// RunTestsFresh is the freshness-aware witness run
// (REQ-evidence-witness-freshness): each expected top-level test whose
// cached fingerprint checks valid against the current tree serves its
// outcomes and registrations from the cache — verification by proven
// equivalence — and only the rest run, per package, with the run's testlog
// captured so the new fingerprints carry the package's observed
// runtime-input manifest. Any fault on the freshness path degrades to the
// full run: the cache saves work, never blocks witnessing.
func RunTestsFresh(dir string) (*verify.TestRun, error) {
	tr, err := runTestsFresh(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "witness cache unavailable (%v); running the full suite\n", err)
		full, ferr := RunTests(dir)
		if ferr != nil {
			return nil, ferr
		}
		full.Degraded = err.Error()
		return full, nil
	}
	return tr, nil
}

func runTestsFresh(dir string) (*verify.TestRun, error) {
	backend, err := New(dir)
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
	pure, err := gofresh.ScanPureDirectivesInWithBuildFlags(dir, buildFlags, pkgs...)
	if err != nil {
		return nil, err
	}
	engine, err := gofresh.New(
		gofresh.WithDir(dir),
		gofresh.WithBuildFlags(buildFlags...),
		gofresh.WithAssumePure(pure),
	)
	if err != nil {
		return nil, err
	}
	engine.Prime(pkgs)

	cached := map[string]witnesscache.Record{}
	for _, rec := range witnesscache.Load(dir) {
		cached[rec.Key()] = rec
	}

	env := goworkEnv(dir)
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true}
	var next []witnesscache.Record

	// One engine pass over every package — check what serves, and capture
	// fingerprints for what must run — before any test executes. Capturing
	// before the run is a correctness constraint: the closure hash must be
	// of the tree that compiles the binary; captured after, an edit made
	// while the tests run would pin pre-edit outcomes under a post-edit
	// hash — a Valid verdict for evidence the current tree never produced.
	// Captured before, the same interleaving records a hash the edited tree
	// no longer matches: Stale, the safe direction.
	type pkgPlan struct {
		pkg   string
		stale []string
		fps   map[string]gofresh.Fingerprint
	}
	var plans []pkgPlan
	for _, pkg := range pkgs {
		var stale []string
		for _, test := range expected[pkg] {
			rec, ok := cached[pkg+"."+test]
			if !ok {
				stale = append(stale, test)
				continue
			}
			verdict, err := engine.Check(rec.Fingerprint.ToGofresh(), gofresh.Subject{Package: pkg, Symbol: test}, dir, gofresh.CodeResult)
			if err != nil || verdict.Status != gofresh.Valid {
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
		}
		if len(stale) == 0 {
			continue
		}
		fps := map[string]gofresh.Fingerprint{}
		for _, test := range stale {
			if fp, err := engine.Capture(gofresh.Subject{Package: pkg, Symbol: test}, dir); err == nil {
				fps[test] = fp
			}
		}
		plans = append(plans, pkgPlan{pkg: pkg, stale: stale, fps: fps})
	}

	// The engine's whole-tree analysis holds gigabytes. Release it — to the
	// OS, not just the Go heap, since the builds it starves are child
	// processes — before spawning race-instrumented builds: witnessing must
	// never run under the analysis's memory pressure; that pressure is
	// exactly what turns transient toolchain faults into degraded runs.
	engine = nil
	debug.FreeOSMemory()

	for _, plan := range plans {
		ran, err := runSelected(dir, env, plan.pkg, plan.stale, tr)
		if err != nil {
			return nil, err
		}
		next = append(next, fingerprintRan(plan.fps, plan.pkg, plan.stale, ran)...)
		for _, test := range plan.stale {
			if _, ok := ran.outcomes[plan.pkg+"."+test]; ok {
				tr.Ran++
			}
		}
	}
	sortRegs(tr)
	if err := witnesscache.EnsureIgnored(dir); err == nil {
		_ = witnesscache.Save(dir, next)
	}
	return tr, nil
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
	manifest string
	digest   string
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
func runSelected(dir string, env []string, pkg string, tests []string, tr *verify.TestRun) (*selectedRun, error) {
	run := &selectedRun{outcomes: map[string]string{}, regs: map[string][]verify.Registration{}, capture: map[string]manifestCapture{}}
	remaining := tests
	for len(remaining) > 0 {
		completed, err := runOnce(dir, env, pkg, remaining, tr, run)
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
func runOnce(dir string, env []string, pkg string, tests []string, tr *verify.TestRun, run *selectedRun) (map[string]bool, error) {
	logf, err := os.CreateTemp("", "stipulator-testlog-*.txt")
	if err != nil {
		return nil, err
	}
	logPath := logf.Name()
	logf.Close()
	defer os.Remove(logPath)

	pattern := "^(" + strings.Join(tests, "|") + ")$"
	// An explicit timeout: the default 10m is per test binary and the
	// freshness witness alone exceeds it under -race; a kill mid-binary
	// masquerades as a test failure.
	args := []string{"test", "-json", "-race", "-timeout=30m", "-run", pattern, pkg, "-args", "-test.testlogfile=" + logPath}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

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
		if pkgDir, ok := packageDir(dir, env, pkg); ok {
			if st, err := runtimeinput.FromTestLog(log, dir, pkgDir); err == nil {
				for t := range completed {
					run.capture[t] = manifestCapture{manifest: st.Manifest, digest: st.Digest}
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

// fingerprintRan assembles the cache records for tests that just ran under
// a clean invocation, from the fingerprints captured before the run. A test
// without a recorded manifest capture (its invocation aborted, or the
// testlog could not be read whole), without an outcome, or whose pre-run
// fingerprint capture failed stays uncached — it simply runs again next
// time.
func fingerprintRan(fps map[string]gofresh.Fingerprint, pkg string, tests []string, run *selectedRun) []witnesscache.Record {
	var out []witnesscache.Record
	for _, test := range tests {
		cap, ok := run.capture[test]
		if !ok {
			continue
		}
		fp, ok := fps[test]
		if !ok {
			continue
		}
		prefix := pkg + "." + test
		outcomes := map[string]string{}
		for key, o := range run.outcomes {
			if key == prefix || strings.HasPrefix(key, prefix+"/") {
				outcomes[key] = o
			}
		}
		if len(outcomes) == 0 {
			continue
		}
		fp.RuntimeInputs, fp.RuntimeDigest = cap.manifest, cap.digest
		out = append(out, witnesscache.Record{
			Package:     pkg,
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
func packageDir(dir string, env []string, pkg string) (string, bool) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", pkg)
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
