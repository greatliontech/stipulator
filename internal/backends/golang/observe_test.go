package golang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func findObservation(observations []*ProcessObservation, pkg string) *ProcessObservation {
	for _, o := range observations {
		if o.Wire.GetPackage() == pkg {
			return o
		}
	}
	return nil
}

// TestGoExecuteObservationOwnership pins per-process observation
// ownership: each launched process owns exactly one observation bound to
// its own producer, a completed observation's manifest names exactly what
// that process observed, and nothing from a sibling process's reads leaks
// into it — no cross-process merging anywhere in the report.
func TestGoExecuteObservationOwnership(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./reads", "./ok"})
	health, _, diags, observations := executeInvocationObserved(t, time.Minute, cfg, "owned")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	if len(observations) != 2 {
		t.Fatalf("invocation owns %d observations, want exactly one per launched process (2)", len(observations))
	}
	readsObs := findObservation(observations, "example.com/exec/reads")
	okObs := findObservation(observations, "example.com/exec/ok")
	if readsObs == nil || okObs == nil {
		t.Fatalf("missing per-package observation: %v", observations)
	}
	pa, pb := readsObs.Wire.GetProducer(), okObs.Wire.GetProducer()
	if pa.GetInvocation() != "owned" || pb.GetInvocation() != "owned" || pa.GetProcessId() <= 0 || pb.GetProcessId() <= 0 {
		t.Errorf("producers not bound to the invocation and real processes: %v %v", pa, pb)
	}
	if pa.GetProcessId() == pb.GetProcessId() && pa.GetProcessOrdinal() == pb.GetProcessOrdinal() {
		t.Errorf("two processes share one identity: %v %v", pa, pb)
	}

	for _, po := range []*ProcessObservation{readsObs, okObs} {
		completed := po.Wire.GetCompleted()
		if completed == nil {
			t.Fatalf("healthy completed process yielded no completed observation: %v", po.Wire)
		}
		if completed.GetManifest() == "" {
			t.Errorf("completed observation for %s carries no manifest", po.Wire.GetPackage())
		}
		// The live gofresh evidence rides beside the wire form and agrees
		// with it.
		state, err := runtimeinput.CompletedState(po.Runtime)
		if err != nil {
			t.Fatalf("live observation for %s is not sealed completed evidence: %v", po.Wire.GetPackage(), err)
		}
		if state.Manifest != completed.GetManifest() {
			t.Errorf("live and wire manifests disagree for %s", po.Wire.GetPackage())
		}
		// A quiet package reading only under its own directory binds: the
		// observation is verifiable and carries its bound digest — the
		// module-relative bracket root covers the recorded reads.
		if state.Unverifiable {
			t.Errorf("quiet package %s sealed unverifiable: %s", po.Wire.GetPackage(), state.Reason)
		}
		if completed.GetDigest() == "" {
			t.Errorf("verifiable observation for %s carries no bound digest", po.Wire.GetPackage())
		}
	}

	readsPaths, err := runtimeinput.ModuleRelPaths(readsObs.Wire.GetCompleted().GetManifest())
	if err != nil {
		t.Fatal(err)
	}
	fixture := "reads/testdata/fixture.txt"
	if !slices.Contains(readsPaths, fixture) {
		t.Errorf("reads observation does not record its own fixture read %s: %v", fixture, readsPaths)
	}
	okPaths, err := runtimeinput.ModuleRelPaths(okObs.Wire.GetCompleted().GetManifest())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(okPaths, fixture) {
		t.Errorf("cross-process leak: ok's observation records reads' fixture %s: %v", fixture, okPaths)
	}
}

// TestGoExecuteKilledMidRunObservationIncomplete pins the fail-closed
// direction: a process that dies mid-run — before the testing runtime
// flushes its testlog — yields an incomplete observation carrying only a
// reason, never a completed record, even though bytes had entered the
// testlog buffer before death.
func TestGoExecuteKilledMidRunObservationIncomplete(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./killmid"})
	health, _, _, observations := executeInvocationObserved(t, time.Minute, cfg, "killmid")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Fatalf("invocation disposition = %v, want TEST_FAILED for a mid-run death", got)
	}
	if len(observations) != 1 {
		t.Fatalf("killed process owns %d observations, want 1", len(observations))
	}
	o := observations[0].Wire
	if o.GetCompleted() != nil {
		t.Fatalf("bytes from an incomplete child became completed evidence: %v", o)
	}
	if o.GetIncompleteReason() == "" {
		t.Error("incomplete observation carries no reason; refusal must be loud")
	}
	if p := o.GetProducer(); p.GetInvocation() != "killmid" || p.GetProcessId() <= 0 {
		t.Errorf("incomplete observation not bound to its producing process: %v", p)
	}
}

// TestGoExecuteUnopenedCaptureObservationIncomplete pins the header
// proof: a package whose TestMain exits green without running the suite
// passes cleanly, yet its testing runtime never opens the capture file —
// the untouched capture must read as a loudly incomplete observation,
// never be ingested as a completed "no runtime inputs observed" record.
func TestGoExecuteUnopenedCaptureObservationIncomplete(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./mainexit"})
	health, _, diags, observations := executeInvocationObserved(t, time.Minute, cfg, "mainexit")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY — the package genuinely passes (diags: %v)", got, diags)
	}
	if len(observations) != 1 {
		t.Fatalf("process owns %d observations, want 1", len(observations))
	}
	o := observations[0].Wire
	if o.GetCompleted() != nil {
		t.Fatalf("an unopened capture became completed evidence: %v", o)
	}
	if reason := o.GetIncompleteReason(); !strings.Contains(reason, "never opened") {
		t.Errorf("incomplete reason = %q, want it to name the unopened capture", reason)
	}
}

// TestGoExecuteAbortOutputBlocksObservation pins the abort wiring end to
// end, from stream bytes to the completeness verdict: abort output in a
// stream that otherwise reads as a clean terminal pass with a clean exit
// stays HEALTHY as suite health, yet disqualifies the process's
// observation — the testlog flush of a process that printed a panic
// marker cannot be trusted, whatever its verdict says.
func TestGoExecuteAbortOutputBlocksObservation(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	stream := `{"Action":"start","Package":"example.com/x"}` + "\n" +
		`{"Action":"output","Package":"example.com/x","Output":"panic: boom\n"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{})
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("disposition = %v, want HEALTHY (abort output is an observation fact, not a suite verdict)", run.disposition)
	}
	reason := incompleteObservationReason(st, nil, run.disposition, "/tmp/log")
	if !strings.Contains(reason, "abort output") {
		t.Fatalf("completeness reason = %q, want the abort disqualification", reason)
	}
}

// TestGoExecuteSubtestAttribution pins deterministic subtest attribution:
// a subtest's outcome and its runtime registrations ride under the exact
// process that produced its parent, never a sibling package's process.
func TestGoExecuteSubtestAttribution(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./reads", "./ok"})
	_, tests, diags, observations := executeInvocationObserved(t, time.Minute, cfg, "subtests")
	sub := findTest(tests, "example.com/exec/reads", "TestReadsFixture/content")
	parent := findTest(tests, "example.com/exec/reads", "TestReadsFixture")
	otherSub := findTest(tests, "example.com/exec/ok", "TestDouble/zero")
	other := findTest(tests, "example.com/exec/ok", "TestDouble")
	if sub == nil || parent == nil || otherSub == nil || other == nil {
		t.Fatalf("missing outcomes (diags: %v)", diags)
	}
	sameProducer := func(a, b *stipulatorv1.ProducerIdentity) bool {
		return a.GetInvocation() == b.GetInvocation() && a.GetProcessId() == b.GetProcessId() && a.GetProcessOrdinal() == b.GetProcessOrdinal()
	}
	if !sameProducer(sub.GetProducer(), parent.GetProducer()) {
		t.Errorf("subtest attributed to a different process than its parent: %v vs %v", sub.GetProducer(), parent.GetProducer())
	}
	if !sameProducer(otherSub.GetProducer(), other.GetProducer()) {
		t.Errorf("subtest attributed to a different process than its parent: %v vs %v", otherSub.GetProducer(), other.GetProducer())
	}
	if sameProducer(sub.GetProducer(), other.GetProducer()) {
		t.Error("two packages' tests share one producing process")
	}
	// The producing process of each outcome is the process owning that
	// package's observation.
	if o := findObservation(observations, "example.com/exec/reads"); o == nil || !sameProducer(sub.GetProducer(), o.Wire.GetProducer()) {
		t.Errorf("subtest producer disagrees with its package observation's producer")
	}
	// The subtest's runtime registration rides the subtest's own result,
	// not its parent's and not a sibling process's.
	if got := sub.GetRegistrations(); !slices.Equal(got, []string{"REQ-exec-reads-probe"}) {
		t.Errorf("subtest registrations = %v, want the probe registration", got)
	}
	if got := parent.GetRegistrations(); len(got) != 0 {
		t.Errorf("parent inherited subtest registrations: %v", got)
	}
	if got := other.GetRegistrations(); len(got) != 0 {
		t.Errorf("sibling process gained registrations: %v", got)
	}
}

// TestGoExecuteResolvedConfigurationBound pins the evidentiary record of
// what actually ran: the invocation health carries the resolved
// pin-at-load configuration — toolchain, platform, cgo, GOFLAGS,
// experiment set, workspace resolution — exactly as normalization pinned
// it at load.
func TestGoExecuteResolvedConfigurationBound(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("resolved")
	inv.SetTimeout(durationpb.New(time.Minute))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	health, _, _, _, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	rc := health.GetGo()
	if rc == nil {
		t.Fatal("invocation health carries no resolved configuration")
	}
	if rc.GetToolchain() != n.Toolchain || !strings.HasPrefix(rc.GetToolchain(), "go") {
		t.Errorf("resolved toolchain = %q, want the pinned %q", rc.GetToolchain(), n.Toolchain)
	}
	if rc.GetGoos() != n.GOOS || rc.GetGoos() == "" || rc.GetGoarch() != n.GOARCH || rc.GetGoarch() == "" {
		t.Errorf("resolved platform = %s/%s, want the pinned %s/%s", rc.GetGoos(), rc.GetGoarch(), n.GOOS, n.GOARCH)
	}
	if rc.GetCgoEnabled() != n.CgoEnabled || rc.GetGoflags() != n.GOFLAGS || rc.GetGoexperiment() != n.GOEXPERIMENT {
		t.Errorf("resolved build config diverges from the pinned invocation: %v vs %+v", rc, n)
	}
	if rc.GetWorkspaceOn() != n.WorkspaceOn || !rc.GetWorkspaceOn() {
		t.Errorf("resolved workspace_on = %v, want the pinned %v (the fixture declares go.work)", rc.GetWorkspaceOn(), n.WorkspaceOn)
	}
}

// TestGoExecuteObservationCompletenessClassifier pins each disqualifying
// fact in isolation: only a healthy disposition over a terminal pass with
// no abort output, no unfinished test, and a clean exit reads as a
// provably flushed testlog; every other shape names its reason.
func TestGoExecuteObservationCompletenessClassifier(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	healthy := stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
	base := func() *streamState {
		return &streamState{terminal: "pass", started: map[string]bool{}}
	}
	for name, tc := range map[string]struct {
		st          *streamState
		waitErr     error
		disposition stipulatorv1.HealthDisposition
		logPath     string
		wantReason  string
	}{
		"clean pass is eligible": {
			st: base(), disposition: healthy, logPath: "/tmp/log", wantReason: "",
		},
		"missing capture file": {
			st: base(), disposition: healthy, logPath: "", wantReason: "testlog capture unavailable",
		},
		"unhealthy disposition": {
			st: base(), disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
			logPath: "/tmp/log", wantReason: "not HEALTHY",
		},
		"no test process (terminal skip)": {
			st:          &streamState{terminal: "skip", started: map[string]bool{}},
			disposition: healthy, logPath: "/tmp/log", wantReason: "no test process ran",
		},
		"abort output": {
			st:          func() *streamState { st := base(); st.sawAbort = true; return st }(),
			disposition: healthy, logPath: "/tmp/log", wantReason: "abort output",
		},
		"started but unfinished": {
			st: func() *streamState {
				st := base()
				st.started["TestX"] = true
				st.startOrder = []string{"TestX"}
				return st
			}(),
			disposition: healthy, logPath: "/tmp/log", wantReason: "started but unfinished",
		},
		"red exit": {
			st: base(), waitErr: errors.New("exit status 1"),
			disposition: healthy, logPath: "/tmp/log", wantReason: "exited with failure",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := incompleteObservationReason(tc.st, tc.waitErr, tc.disposition, tc.logPath)
			if tc.wantReason == "" {
				if got != "" {
					t.Fatalf("eligible shape refused: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantReason) {
				t.Fatalf("reason = %q, want it to name %q", got, tc.wantReason)
			}
		})
	}
}

// selfMutatingModule stages a temporary module whose one test reads its
// committed input and rewrites the same bytes in place before returning:
// the content is unchanged, but the rewrite persists past process exit
// and moves the input's recorded metadata — the run-to-ingest window
// made durable by the run itself, which the pre-spawn observation
// bracket must catch. Staged into a temp directory, never committed: an
// in-place fixture would move this package's own bracket on every run.
func selfMutatingModule(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod":                 "module example.com/selfmut\n\ngo 1.24\n",
		"mut/testdata/input.txt": "self-mutated-bytes\n",
		"mut/mut_test.go": `package mut

import (
	"os"
	"testing"
)

func TestRewritesOwnInput(t *testing.T) {
	raw, err := os.ReadFile("testdata/input.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("testdata/input.txt", raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
	})
}

// TestGoExecuteSelfMutatedInputSealsUnverifiable pins the run-to-ingest
// window: a test that rewrites its own observed input mid-run — the
// mutation persisting past process exit — moves the pre-spawn observation
// bracket, so the completed observation seals unverifiable naming the
// moved root and carries no bound digest. The failure direction is
// re-execution, never reuse.
func TestGoExecuteSelfMutatedInputSealsUnverifiable(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	neutralAmbient(t)
	tmp := selfMutatingModule(t)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("selfmut")
	inv.SetTimeout(durationpb.New(time.Minute))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./mut"})
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, tmp, inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	health, _, diags, observations, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY — the suite genuinely passes (diags: %v)", got, diags)
	}
	o := findObservation(observations, "example.com/selfmut/mut")
	if o == nil || o.Wire.GetCompleted() == nil {
		t.Fatalf("healthy completed process yielded no completed observation: %v", observations)
	}
	state, err := runtimeinput.CompletedState(o.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable {
		t.Fatal("mid-run mutation of a bracketed input did not seal the observation unverifiable")
	}
	if !strings.Contains(state.Reason, "observation bracket moved") || !strings.Contains(state.Reason, "mut") {
		t.Errorf("unverifiable reason = %q, want the moved bracket root named", state.Reason)
	}
	if o.Wire.GetCompleted().GetDigest() != "" {
		t.Error("unverifiable observation carries a bound digest; a moved bracket must never bind")
	}
	paths, err := runtimeinput.ModuleRelPaths(o.Wire.GetCompleted().GetManifest())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, "mut/testdata/input.txt") {
		t.Errorf("mutated input not recorded in the manifest: %v", paths)
	}
}

// TestGoExecuteModuleRootPackageBracket pins the "." root shape: a tree
// whose root directory is itself the selected package declares the whole
// tree as its bracket root. Quiet, the observation binds; a write landing
// anywhere under the tree during the span — a sibling data directory
// here, and equally the tree's own bookkeeping directory (`.stipulator`),
// which is deliberately not bracket-excluded — moves the bracket and
// seals unverifiable. The module-root shape trades root narrowness for
// tree-wide sensitivity: the failure direction is re-execution, never a
// wrong bind.
func TestGoExecuteModuleRootPackageBracket(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	neutralAmbient(t)
	execRoot := func(t *testing.T, files map[string]string) (*ProcessObservation, runtimeinput.State) {
		t.Helper()
		tmp := writeModule(t, files)
		inv := &stipulatorv1.PolicyInvocation{}
		inv.SetName("rootpkg")
		inv.SetTimeout(durationpb.New(time.Minute))
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"."})
		inv.SetGo(cfg)
		ctx := context.Background()
		n, err := NormalizeInvocation(ctx, tmp, inv)
		if err != nil {
			t.Fatal(err)
		}
		obs, err := DiscoverInvocation(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		health, _, diags, observations, err := ExecuteInvocation(ctx, n, obs)
		if err != nil {
			t.Fatal(err)
		}
		if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
		}
		o := findObservation(observations, "example.com/rootpkg")
		if o == nil || o.Wire.GetCompleted() == nil {
			t.Fatalf("healthy completed process yielded no completed observation: %v", observations)
		}
		state, err := runtimeinput.CompletedState(o.Runtime)
		if err != nil {
			t.Fatal(err)
		}
		return o, state
	}
	t.Run("quiet root binds", func(t *testing.T) {
		o, state := execRoot(t, map[string]string{
			"go.mod":               "module example.com/rootpkg\n\ngo 1.24\n",
			"testdata/fixture.txt": "root-bytes\n",
			"root_test.go": `package rootpkg

import (
	"os"
	"testing"
)

func TestQuietRead(t *testing.T) {
	if _, err := os.ReadFile("testdata/fixture.txt"); err != nil {
		t.Fatal(err)
	}
}
`,
		})
		if state.Unverifiable {
			t.Fatalf("quiet module-root package sealed unverifiable: %s", state.Reason)
		}
		if o.Wire.GetCompleted().GetDigest() == "" {
			t.Error("verifiable module-root observation carries no bound digest")
		}
	})
	t.Run("sibling write within the tree moves the root bracket", func(t *testing.T) {
		o, state := execRoot(t, map[string]string{
			"go.mod":           "module example.com/rootpkg\n\ngo 1.24\n",
			"sibling/data.txt": "before\n",
			"root_test.go": `package rootpkg

import (
	"os"
	"testing"
)

func TestWritesSibling(t *testing.T) {
	if err := os.WriteFile("sibling/data.txt", []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
}
`,
		})
		if !state.Unverifiable {
			t.Fatal("a mid-span write under the module-root bracket sealed verifiable")
		}
		if !strings.Contains(state.Reason, "observation bracket moved") {
			t.Errorf("unverifiable reason = %q, want the moved bracket named", state.Reason)
		}
		if o.Wire.GetCompleted().GetDigest() != "" {
			t.Error("unverifiable observation carries a bound digest")
		}
	})
}

// TestGoExecuteOutOfRootReadSealsUnverifiable pins the declared root
// policy's consequence: a read resolving inside the verification tree but
// outside the package directory — the observation bracket's one declared
// root — seals per-identity unverifiable, permanently uncacheable, and
// the completed observation carries no bound digest. A test wanting
// cacheable fixtures keeps them under its own package directory.
func TestGoExecuteOutOfRootReadSealsUnverifiable(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-policy-attribution")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./extread"})
	health, _, diags, observations := executeInvocationObserved(t, time.Minute, cfg, "extread")
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	o := findObservation(observations, "example.com/exec/extread")
	if o == nil || o.Wire.GetCompleted() == nil {
		t.Fatalf("healthy completed process yielded no completed observation: %v", observations)
	}
	state, err := runtimeinput.CompletedState(o.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unverifiable {
		t.Fatal("out-of-root read did not seal the observation unverifiable")
	}
	if !strings.Contains(state.Reason, "not covered by observation bracket") ||
		!strings.Contains(state.Reason, "reads/testdata/fixture.txt") {
		t.Errorf("unverifiable reason = %q, want the uncovered identity named", state.Reason)
	}
	if o.Wire.GetCompleted().GetDigest() != "" {
		t.Error("unverifiable observation carries a bound digest; an uncovered read must never bind")
	}
}

// TestGoExecuteUnknownPackageDirObservationIncomplete pins the fail-closed
// leg of pre-spawn bracket capture: a package whose directory discovery
// never resolved gets no bracket, so even a provably flushed testlog
// yields an incomplete observation naming the missing directory — never a
// completed record sealed without a bracket.
func TestGoExecuteUnknownPackageDirObservationIncomplete(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("nodirs")
	inv.SetTimeout(durationpb.New(time.Minute))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately no discovery: the executor must fail closed when the
	// package-directory map was never populated.
	selection := []Obligation{{Kind: ObligationPackage, Package: "example.com/exec/ok"}}
	health, _, diags, observations, err := ExecuteInvocation(ctx, n, selection)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	if len(observations) != 1 {
		t.Fatalf("process owns %d observations, want 1", len(observations))
	}
	o := observations[0].Wire
	if o.GetCompleted() != nil {
		t.Fatalf("a bracketless process produced completed evidence: %v", o)
	}
	if reason := o.GetIncompleteReason(); !strings.Contains(reason, "package directory unknown") {
		t.Errorf("incomplete reason = %q, want the unresolved package directory named", reason)
	}
}

// TestGoExecuteExternalWorkspaceMemberObservationIncomplete pins the
// external-member leg: a workspace member outside the verification tree
// has no module-relative bracket root, so its process — however cleanly
// it completes — owns an incomplete observation naming the out-of-tree
// directory, never an unbracketed completed record.
func TestGoExecuteExternalWorkspaceMemberObservationIncomplete(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution")
	neutralAmbient(t)
	parent := t.TempDir()
	tree := filepath.Join(parent, "tree")
	for path, content := range map[string]string{
		"tree/go.work": "go 1.24\n\nuse (\n\t.\n\t../ext\n)\n",
		"tree/go.mod":  "module example.com/tree\n\ngo 1.24\n",
		"ext/go.mod":   "module example.com/extmod\n\ngo 1.24\n",
		"ext/ext_test.go": `package extmod

import "testing"

func TestExternal(t *testing.T) {}
`,
	} {
		full := filepath.Join(parent, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("external")
	inv.SetTimeout(durationpb.New(time.Minute))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"example.com/extmod"})
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, tree, inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	health, _, diags, observations, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	o := findObservation(observations, "example.com/extmod")
	if o == nil {
		t.Fatalf("external member owns no observation: %v", observations)
	}
	if o.Wire.GetCompleted() != nil {
		t.Fatalf("an out-of-tree member produced completed evidence: %v", o.Wire)
	}
	if reason := o.Wire.GetIncompleteReason(); !strings.Contains(reason, "outside the verification tree") {
		t.Errorf("incomplete reason = %q, want the out-of-tree directory named", reason)
	}
}

// TestGoExecuteSymlinkedTreeObservationBinds pins the resolved
// observation frame: a verification tree reached through a symlinked
// prefix — `go list` reports resolved package directories while the
// invocation carries the alias — must still bracket and bind. A lexical
// containment check would misclassify every package as outside the tree
// (incomplete observations with a misleading reason), and an unresolved
// module view would classify every recorded read as external
// (per-identity unverifiable) — either way total, silent cache death.
func TestGoExecuteSymlinkedTreeObservationBinds(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-attribution", "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	parent := t.TempDir()
	for path, content := range map[string]string{
		"real/go.mod":                   "module example.com/aliased\n\ngo 1.24\n",
		"real/pkg/testdata/fixture.txt": "aliased-bytes\n",
		"real/pkg/pkg_test.go": `package pkg

import (
	"os"
	"testing"
)

func TestReadsFixture(t *testing.T) {
	if _, err := os.ReadFile("testdata/fixture.txt"); err != nil {
		t.Fatal(err)
	}
}
`,
	} {
		full := filepath.Join(parent, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(filepath.Join(parent, "real"), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("aliased")
	inv.SetTimeout(durationpb.New(time.Minute))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, alias, inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	health, _, diags, observations, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("invocation disposition = %v, want HEALTHY (diags: %v)", got, diags)
	}
	o := findObservation(observations, "example.com/aliased/pkg")
	if o == nil {
		t.Fatalf("aliased package owns no observation: %v", observations)
	}
	if o.Wire.GetCompleted() == nil {
		t.Fatalf("symlinked-tree observation incomplete: %q", o.Wire.GetIncompleteReason())
	}
	state, err := runtimeinput.CompletedState(o.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if state.Unverifiable {
		t.Fatalf("symlinked-tree observation sealed unverifiable: %s", state.Reason)
	}
	if o.Wire.GetCompleted().GetDigest() == "" {
		t.Error("symlinked-tree observation carries no bound digest")
	}
	paths, err := runtimeinput.ModuleRelPaths(o.Wire.GetCompleted().GetManifest())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, "pkg/testdata/fixture.txt") {
		t.Errorf("fixture read not recorded module-relative: %v", paths)
	}
}

// TestGoExecuteRefusesPostTerminalEvents pins the refusal ladder for
// streams that outlive their own verdict: any event after the terminal
// package event degrades the package loudly — the toolchain never
// produces that shape, so it is refused rather than trusted.
func TestGoExecuteRefusesPostTerminalEvents(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	stream := `{"Action":"start","Package":"example.com/x"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x"}` + "\n" +
		`{"Action":"run","Package":"example.com/x","Test":"TestLate"}` + "\n" +
		`{"Action":"pass","Package":"example.com/x","Test":"TestLate"}` + "\n"
	st := parseTestStream("inv", "example.com/x", strings.NewReader(stream), nil)
	run := classifyRun("inv", "example.com/x", st, nil, &boundedBuffer{})
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("disposition = %v, want DEGRADED for events after the terminal package event", run.disposition)
	}
	if len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), "after the terminal package event") {
		t.Errorf("post-terminal refusal not named in the diagnostic: %v", run.diags)
	}
}
