package golang

import (
	"context"
	"errors"
	"fmt"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/closure"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
)

// The engine choke point refuses toolchain-provenance skew before any
// verdict: an ambient toolchain this binary's compiled-in frontend
// cannot faithfully read (newer within the major, or another major)
// must never be judged (gofresh.ToolchainSkew). The sample resolves in
// the tree root under the GROUP's normalized environment — the same
// resolution the group's loads and executions use, its GOTOOLCHAIN pin
// included.
func TestGroupEngineRefusesToolchainSkew(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	var sampledDir string
	var sampledEnv []string
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		sampledDir = dir
		sampledEnv = env
		return "go99.1.0", nil
	}
	probes := 0
	priorProbe := probeObserverForTest
	probeObserverForTest = func(*exec.Cmd) { probes++ }
	t.Cleanup(func() { probeObserverForTest = priorProbe })
	dir := t.TempDir()
	g := &captureGroup{env: append(os.Environ(), "STIPULATOR_PROVENANCE_PROBE=1")}
	if _, err := groupEngine(t.Context(), dir, g); err == nil {
		t.Fatal("groupEngine accepted a toolchain a whole major ahead of the binary")
	} else if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("skew refusal = %v, want the cross-major class named", err)
	} else {
		var pe *gofresh.ToolchainProvenanceError
		if !errors.As(err, &pe) {
			t.Fatalf("refusal %v is not a *gofresh.ToolchainProvenanceError", err)
		}
	}
	if sampledDir != dir {
		t.Fatalf("read for dir = %q, want the tree root %q", sampledDir, dir)
	}
	probed := false
	for _, kv := range sampledEnv {
		if kv == "STIPULATOR_PROVENANCE_PROBE=1" {
			probed = true
		}
	}
	if !probed {
		t.Fatal("the read did not carry the group's environment")
	}
	// The engine arm judges the normalization's one read: no sampler
	// spawns a second time.
	if probes != 0 {
		t.Fatalf("the engine arm spawned %d probes, want the normalization's read alone", probes)
	}
	// With no seam the group's own read decides: a group carrying a
	// cross-major normalization read refuses, and nothing is spawned to
	// second-guess it — a sampler minted here would ask the host and
	// find the binary's own toolchain.
	toolchainSampleForTest = nil
	probes = 0
	if _, err := groupEngine(t.Context(), dir, &captureGroup{env: os.Environ(), toolchain: "go99.1.0"}); err == nil || !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("a group carrying a cross-major read: %v, want the cross-major refusal", err)
	}
	if probes != 0 {
		t.Fatalf("the engine arm spawned %d probes over the group's own read", probes)
	}
}

// An unidentifiable toolchain refuses fail-closed, a failed read
// classifies identically — unidentifiable is not agreement — and so
// does a group whose normalization read nothing.
func TestGroupEngineRefusesUnidentifiableToolchain(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "devel +abc123", nil
	}
	if _, err := groupEngine(t.Context(), t.TempDir(), &captureGroup{}); err == nil {
		t.Fatal("groupEngine accepted an unidentifiable toolchain")
	} else if !strings.Contains(err.Error(), "unidentifiable") || !strings.Contains(err.Error(), "binary built with") || !strings.Contains(err.Error(), closure.AnalyzingFrontend()) {
		t.Fatalf("refusal = %v, want the composite's unidentifiable refusal naming the frontend", err)
	}
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "", fmt.Errorf("boom")
	}
	var pe *gofresh.ToolchainProvenanceError
	if _, err := groupEngine(t.Context(), t.TempDir(), &captureGroup{}); !errors.As(err, &pe) {
		t.Fatalf("read-failure refusal %v is not a *gofresh.ToolchainProvenanceError", err)
	}
	toolchainSampleForTest = nil
	if _, err := groupEngine(t.Context(), t.TempDir(), &captureGroup{toolchain: ""}); !errors.As(err, &pe) {
		t.Fatalf("an empty normalization read %v is not a *gofresh.ToolchainProvenanceError", err)
	}
}

// classifyFault routes exactly the provenance class to a run-level
// abort; every other fault stays a degradation reason.
func TestClassifyFaultRoutesProvenanceToAbort(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	if abort, _ := classifyFault(&gofresh.ToolchainProvenanceError{Err: fmt.Errorf("skew")}); !abort {
		t.Fatal("provenance refusal did not classify as an abort")
	}
	if abort, reason := classifyFault(fmt.Errorf("view fault")); abort || reason != "view fault" {
		t.Fatalf("ordinary fault classified abort=%v reason=%q", abort, reason)
	}
	if abort, _ := classifyFault(fmt.Errorf("wrapped: %w", &gofresh.ToolchainProvenanceError{Err: fmt.Errorf("skew")})); !abort {
		t.Fatal("wrapped provenance refusal did not classify as an abort")
	}
}

// The directional contract, both arms: a binary NEWER than the ambient
// series within one major reads it (the Go 1 promise — a declared
// older toolchain measures from a current binary), while an ambient
// series newer than the binary's refuses (the frontend predates the
// sources).
func TestCheckToolchainProvenanceDirectional(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })

	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "go1.1.0", nil
	}
	if err := checkToolchainProvenance(context.Background(), t.TempDir(), nil, newToolchainSample()); err != nil {
		t.Fatalf("older-within-major ambient refused: %v", err)
	}

	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "go1.99999.0", nil
	}
	err := checkToolchainProvenance(context.Background(), t.TempDir(), nil, newToolchainSample())
	if err == nil {
		t.Fatal("newer-within-major ambient accepted — the frontend predates its sources")
	}
	if !strings.Contains(err.Error(), "predates") {
		t.Fatalf("refusal = %v, want the predates-the-sources class named", err)
	}
}

// A toolchain-provenance refusal ABORTS the serving run — it must
// never surface as the degraded full execution, which would run a
// suite the refused frontend discovered and selected
// (REQ-evidence-toolchain-provenance vs REQ-evidence-freshness-degrade).
func TestRunWitnessesAbortsOnToolchainSkew(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	if testing.Short() {
		t.Skip("runs policy discovery")
	}
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/freshfixture")); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)
	tr, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err == nil {
		t.Fatalf("skewed serving run did not abort; degraded run = %+v", tr)
	}
	if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("abort = %v, want the skew refusal", err)
	}
}

// The recorder path aborts identically: a skewed frontend must not
// prepare a witnessed suite execution.
func TestNewWitnessRecorderAbortsOnToolchainSkew(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	if testing.Short() {
		t.Skip("runs policy discovery")
	}
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/freshfixture")); err != nil {
		t.Fatal(err)
	}
	writeRacePolicy(t, tmp)
	p, _, err := policy.Load(tmp, map[string]policy.Backend{"go": Policy{}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := NewWitnessRecorder(context.Background(), mustCapture(t, context.Background(), tmp, p), noSeeding{})
	if err == nil {
		t.Fatalf("skewed recorder did not abort; degraded = %q", rec.degraded)
	}
	if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("abort = %v, want the skew refusal", err)
	}
}

// The selection-view arm: a build selection's package-load view is a
// frontend parse too, so newContext inherits the prerequisite — a
// toolchain the frontend cannot read refuses the binding context
// outright, never a degraded view (REQ-evidence-toolchain-provenance's
// selection arm).
func TestNewContextRefusesToolchainSkew(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	if testing.Short() {
		t.Skip("runs go list")
	}
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	dir := buildSelectionModule(t)
	if _, err := newContext(context.Background(), dir, nil); err == nil {
		t.Fatal("skewed binding context did not refuse")
	} else if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("refusal = %v, want the skew class named", err)
	}
}

// The sample resolves where the loads and witnesses do: every selection
// view samples in each member's own directory, and a group's engine in
// the group's module root — under GOTOOLCHAIN=auto the selected
// toolchain is per module. The failure rule keeps its arm split: the
// record-judging engine arm refuses an unidentifiable toolchain, while
// the selection arms leave an unsampleable member to its loads and
// refuse only an identified skew.
//
//gofresh:pure
func TestToolchainSampledInTheTargetModule(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the workspace fixture")
	}
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	var dirs []string
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		dirs = append(dirs, dir)
		return runtime.Version(), nil
	}
	if _, err := newContext(context.Background(), "testdata/workspacemod", nil); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("testdata/workspacemod")
	seen := map[string]bool{}
	for _, d := range dirs {
		abs, _ := filepath.Abs(d)
		seen[abs] = true
	}
	for _, want := range []string{root, filepath.Join(root, "sub")} {
		if !seen[want] {
			t.Fatalf("member %s never sampled; sampled %v", want, dirs)
		}
	}

	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/workspacemod")); err != nil {
		t.Fatal(err)
	}
	derived, err := DerivePolicy(tmp)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := mustCapture(t, context.Background(), tmp, derived).discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var nested *captureGroup
	for _, g := range pc.groups {
		if g.moduleRoot == "sub" {
			nested = g
		}
	}
	if nested == nil {
		t.Fatalf("no group rooted at the nested member; roots %v", pc.groups)
	}
	// The group's engine judges the GOVERSION its invocation's
	// normalization read in the nested module root (the group carries
	// it; the seam answers for it here) and spawns no probe itself.
	if nested.toolchain != runtime.Version() {
		t.Fatalf("the nested group carries toolchain %q, want the normalization's read %q", nested.toolchain, runtime.Version())
	}
	dirs = nil
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		dirs = append(dirs, dir)
		return "", errors.New("go: no toolchain")
	}
	probes := 0
	priorProbe := probeObserverForTest
	probeObserverForTest = func(*exec.Cmd) { probes++ }
	t.Cleanup(func() { probeObserverForTest = priorProbe })
	_, err = groupEngine(context.Background(), tmp, nested)
	if err == nil || !strings.Contains(err.Error(), "unidentifiable") {
		t.Fatalf("unsampleable group engine = %v, want the unidentifiable refusal", err)
	}
	if len(dirs) != 1 || dirs[0] != filepath.Join(tmp, "sub") {
		t.Fatalf("group engine read for %v, want the group's module root", dirs)
	}
	if probes != 0 {
		t.Fatalf("group engine spawned %d probes, want the normalization's read alone", probes)
	}
	// The selection arms keep the per-view rule: a member whose
	// toolchain cannot be sampled is left to its view (the default
	// view loads; binding stays healthy), and an identified skew
	// still refuses the run.
	if _, err := newContext(context.Background(), tmp, nil); err != nil {
		t.Fatalf("unsampleable member failed the binding context: %v (want the per-view degradation)", err)
	}
	if _, err := selectionEngine(context.Background(), tmp, buildSelection{}, newToolchainSample()); err != nil {
		t.Fatalf("unsampleable member failed the served selection engine: %v", err)
	}
	dirs = nil
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		dirs = append(dirs, dir)
		return runtime.Version(), nil
	}
	if _, err := selectionEngine(context.Background(), tmp, buildSelection{}, newToolchainSample()); err != nil {
		t.Fatal(err)
	}
	seen = map[string]bool{}
	for _, d := range dirs {
		seen[d] = true
	}
	for _, want := range []string{tmp, filepath.Join(tmp, "sub")} {
		if !seen[want] {
			t.Fatalf("served selection engine never sampled %s; sampled %v", want, dirs)
		}
	}
	toolchainSampleForTest = func(context.Context, string, []string) (string, error) { return "go99.1.0", nil }
	if _, err := selectionEngine(context.Background(), tmp, buildSelection{}, newToolchainSample()); err == nil || !strings.Contains(err.Error(), "toolchain provenance") {
		t.Fatalf("skewed served selection engine = %v, want the skew refusal", err)
	}
}

// The provenance probe runs in the caller's own process group
// (REQ-go-owned-processes-runner: a descendant-free query, swept with its
// caller by the owner that kills the caller outright) with the reap
// bounded (REQ-policy-cancellation), through gofresh's memoized sampler:
// two asks of one (directory, environment) spawn one `go env GOVERSION`
// (the test seam counts the prepared commands), the sample is the
// trimmed version, a cancelled operation samples nothing and the member
// walk answers a cancelled context whatever the memo holds.
func TestProvenanceProbeRunsInTheCallersGroup(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes-runner", "REQ-policy-cancellation")
	env, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := probeRunner.Command(context.Background(), ".", env, "env", "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.SysProcAttr != nil || cmd.WaitDelay != probeWaitDelay {
		t.Fatalf("probe = attr %+v, wait delay %s; want the caller's group under the bounded reap", cmd.SysProcAttr, cmd.WaitDelay)
	}
	// The memo: two asks, one spawn — counted by a runner sharing the
	// probe's preparation (a sample is no derivation spawn, so the
	// derivation seam never sees one; the seam's reuse pins count
	// exactly the derivation's).
	spawns := 0
	counting := probeRunner
	counting.Prepare = func(cmd *exec.Cmd) {
		boundProbe(cmd)
		spawns++
	}
	commandHook = func(name string, args []string) {
		if len(args) > 1 && args[0] == "env" && args[1] == "GOVERSION" {
			t.Fatalf("the probe reached the derivation seam: %s %v", name, args)
		}
	}
	t.Cleanup(func() { commandHook = nil })
	sampler := (&gotool.Sampler{Runner: counting}).Sample
	for range 2 {
		v, err := sampler(context.Background(), ".", env)
		if err != nil || !strings.HasPrefix(v, "go") || strings.ContainsAny(v, " \n\t") {
			t.Fatalf("sample = %q, %v; want a trimmed go version", v, err)
		}
	}
	if spawns != 1 {
		t.Fatalf("two asks spawned %d samples, want one — the memo answers the second", spawns)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sampler(cancelled, ".", env); err == nil || spawns != 1 {
		t.Fatalf("a cancelled operation sampled: spawns %d, err %v", spawns, err)
	}
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	toolchainSampleForTest = func(context.Context, string, []string) (string, error) { return runtime.Version(), nil }
	if err := checkSelectionMembers(context.Background(), ".", nil, []string{"."}, "", newToolchainSample()); err != nil {
		t.Fatal(err)
	}
	if err := checkSelectionMembers(cancelled, ".", nil, []string{"."}, "", newToolchainSample()); err == nil {
		t.Fatal("a cancelled member walk answered from the memo")
	}
}

// The toolchain sampler is one judged operation's: two served backends
// over one tree each mint their own memo, so the second's first
// selection engine samples the toolchain again where a process-wide
// memo would have served the first's answer for the process's life —
// the MCP server builds a served backend per call (gofresh's
// toolchain-skew clause bounds a sampler to one judged run). A capture
// group's engine, by contrast, judges the normalization's one read and
// samples nothing.
//
// Deliberately not //gofresh:pure: the sample shells the go toolchain.
func TestToolchainSamplerIsMintedPerOperation(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	if testing.Short() {
		t.Skip("loads a fixture module's types and views")
	}
	neutralAmbient(t)
	dir := servedModule(t)
	var mu sync.Mutex
	probes := 0
	prior := probeObserverForTest
	probeObserverForTest = func(*exec.Cmd) {
		mu.Lock()
		probes++
		mu.Unlock()
	}
	t.Cleanup(func() { probeObserverForTest = prior })
	for round := 1; round <= 2; round++ {
		s, err := NewServed(context.Background(), dir, servedSymbols)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		before := probes
		mu.Unlock()
		// The type has no record on any round, so the ask types it
		// through the child and the backend publishes at its close,
		// where its selection engine is built; the window spans the
		// backend's whole life.
		ask(t, s, "example.com/served/p.T")
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		sampled := probes - before
		mu.Unlock()
		if sampled == 0 {
			t.Fatalf("served backend %d sampled the toolchain %d times, want its own sample: a memo outlived its operation", round, sampled)
		}
	}
}

// TestToolchainPinRuleIsTheGotoolchainGrammar pins the one rule both
// arms read, as cmd/go reads the grammar: local/path/auto and the
// local+ forms require nothing; a bare name is equality; +auto/+path
// select the name or the module file's newer requirement, the resolved
// side equal to it in Go's version grammar (a vendor suffix its
// release); a `toolchain default` file reads as no requirement; a
// raised selection names its file; an unreadable side refuses naming
// the side, with the remedy a pin can follow; every refusal names the
// pin, the resolved toolchain and the remedy
// (REQ-policy-toolchain-pin).
func TestToolchainPinRuleIsTheGotoolchainGrammar(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-toolchain-pin")
	req := func(toolchain string) moduleRequirement {
		if toolchain == "" {
			return moduleRequirement{}
		}
		return moduleRequirement{toolchain: toolchain, file: "go.mod"}
	}
	for _, pin := range []string{"", "local", "path", "auto", "local+auto", "local+path"} {
		if err := toolchainPinSatisfied(pin, "go1.27.0-dst.14", req("go1.28.0")); err != nil {
			t.Fatalf("pin %q over a vendor build: %v", pin, err)
		}
	}
	if err := toolchainPinSatisfied("go1.26.5-dst.6", "go1.26.5-dst.6", req("")); err != nil {
		t.Fatalf("an equal vendor pin: %v", err)
	}
	remedy := "install the pinned toolchain, or declare the resolved one in the accepted policy"
	err := toolchainPinSatisfied("go1.26.5-dst.6", "go1.27.0-dst.14", req(""))
	if err == nil || !strings.Contains(err.Error(), `pin "go1.26.5-dst.6"`) || !strings.Contains(err.Error(), `resolves "go1.27.0-dst.14"`) || !strings.Contains(err.Error(), remedy) {
		t.Fatalf("the field report's shape = %v", err)
	}
	// A selection: the name where the module requires nothing newer,
	// else the requirement; the resolved side equal to it, the grammar
	// reading a vendor suffix as its release; a `toolchain default`
	// file (no requirement) leaves the name alone whatever its go line.
	for _, c := range []struct{ pin, resolved, required string }{
		{"go1.26.5+auto", "go1.26.5", ""},
		{"go1.26.5+auto", "go1.26.5-dst.6", ""},
		{"go1.26.5+auto", "go1.27.1", "go1.27.1"},
		{"go1.26.5+path", "go1.27.0", "go1.27.0"},
		{"go1.27.1+auto", "go1.27.1", "go1.26.0"},
		{"go1.27.0-dst.15+auto", "go1.27.0-dst.14", ""},
		{"go1.23.0+auto", "go1.23.0", ""},
	} {
		if err := toolchainPinSatisfied(c.pin, c.resolved, req(c.required)); err != nil {
			t.Fatalf("selection %+v: %v", c, err)
		}
	}
	// The field report's mechanism under a +auto pin: the wrapper
	// resolves the local toolchain where the selection is the name — a
	// requirement below the name raises nothing and names no file.
	err = toolchainPinSatisfied("go1.26.5-dst.6+auto", "go1.27.0-dst.14", req("go1.24.0"))
	if err == nil || !strings.Contains(err.Error(), `the pin select "go1.26.5-dst.6"`) || !strings.Contains(err.Error(), `resolves "go1.27.0-dst.14"`) || !strings.Contains(err.Error(), remedy) {
		t.Fatalf("a wrapper under a +auto pin = %v", err)
	}
	for _, c := range []struct{ pin, resolved, required string }{
		{"go1.27.1+auto", "go1.27.0", ""},
		{"go1.27.1+path", "go1.27.0", ""},
		{"go1.26.5+auto", "go1.27.1", "go1.27.0"},
	} {
		if err := toolchainPinSatisfied(c.pin, c.resolved, req(c.required)); err == nil || !strings.Contains(err.Error(), "is not satisfied") {
			t.Fatalf("a resolved side off the selection %+v = %v", c, err)
		}
	}
	// A requirement that raised the selection names its file.
	if err := toolchainPinSatisfied("go1.26.5+auto", "go1.26.5", moduleRequirement{toolchain: "go1.27.0", file: "/w/go.work"}); err == nil || !strings.Contains(err.Error(), `the pin and /w/go.work's requirement select "go1.27.0"`) {
		t.Fatalf("a raised selection = %v, want the file named", err)
	}
	// An unreadable side names the side and a remedy a pin can follow.
	if err := toolchainPinSatisfied("go1.26.5+auto", "devel go1.28-abc", req("")); err == nil || !strings.Contains(err.Error(), `resolves "devel go1.28-abc"`) || !strings.Contains(err.Error(), "pin `local`") {
		t.Fatalf("an unreadable resolved side = %v", err)
	}
	if err := toolchainPinSatisfied("devel go1.28-abc+auto", "go1.27.1", req("")); err == nil || !strings.Contains(err.Error(), "names a minimum") || !strings.Contains(err.Error(), "pin a release the grammar reads") {
		t.Fatalf("an unreadable pin name = %v", err)
	}
	if err := toolchainPinSatisfied("go1.26.5+auto", "go1.27.0 X:nodwarf5", req("")); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("an experiment-stamped resolved side = %v", err)
	}
}

// TestToolchainRequirementReadsTheModuleFileAsCmdGoDoes pins the
// requirement a +auto/+path selection may upgrade to, read as the go
// command reads it: a workspace's go.work over the member's go.mod, the
// lines found by a line scan (a directive this build does not know and
// a trailing comment change nothing; an indented or a differently
// spelled line is not the directive), the toolchain line over the go
// line, `toolchain default` no requirement at all, a bare language
// version from go1.21 on its ".0" release, the file named, and nothing
// where no file is read (REQ-policy-toolchain-pin).
func TestToolchainRequirementReadsTheModuleFileAsCmdGoDoes(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-toolchain-pin")
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for body, want := range map[string]string{
		"module m\n\ngo 1.24\n":                                        "go1.24.0",
		"module m\n\ngo 1.24.3\n":                                      "go1.24.3",
		"module m\n\ngo 1.20\n":                                        "go1.20",
		"module m\n\ngo 1.24\n\ntoolchain go1.25.1\n":                  "go1.25.1",
		"module m\n\ngo 1.26.0\n\ntoolchain go1.25.1\n":                "go1.26.0",
		"module m\n\ngo 1.24\n\ntoolchain go1.25.1-dst.3\n":            "go1.25.1-dst.3",
		"module m\n\ngo 1.24\n\ntoolchain default\n":                   "",
		"module m\n\ngo 1.24 // the language\n\nfuturedirective x y\n": "go1.24.0",
		"module m\n\n\tgo 1.25\n\ngo 1.24\n":                           "go1.25.0",
		"module m\n\ngolang 1.25\n\ngo\t1.24\n":                        "go1.24.0",
	} {
		dir := t.TempDir()
		write(dir, "go.mod", body)
		got := toolchainRequirement("", dir)
		if got.toolchain != want || (want != "" && got.file != filepath.Join(dir, "go.mod")) || (want == "" && got.file != "") {
			t.Errorf("go.mod %q: requirement = %+v, want %q from the file", body, got, want)
		}
	}
	if got := toolchainRequirement("", t.TempDir()); got != (moduleRequirement{}) {
		t.Errorf("no go.mod: requirement = %+v", got)
	}
	dir := t.TempDir()
	write(dir, "go.mod", "module m\n\ngo 1.26.0\n")
	write(dir, "go.work", "go 1.24\n\ntoolchain go1.25.0\n\nuse .\n")
	if got := toolchainRequirement(filepath.Join(dir, "go.work"), dir); got.toolchain != "go1.25.0" || got.file != filepath.Join(dir, "go.work") {
		t.Errorf("go.work over go.mod: requirement = %+v, want the workspace's from go.work", got)
	}
}

// TestSelectionViewsRefuseAnUnsatisfiedToolchainPin pins the selection
// arms: an identified member toolchain the selection's pin does not
// admit refuses the walk naming the member, a satisfied pin walks on,
// and a member whose sample fails stays the view's own degradation
// (REQ-policy-toolchain-pin).
func TestSelectionViewsRefuseAnUnsatisfiedToolchainPin(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-toolchain-pin")
	sample := func(_ context.Context, dir string, env []string) (string, error) {
		if strings.HasSuffix(dir, "broken") {
			return "", fmt.Errorf("boom")
		}
		return "go1.27.1", nil
	}
	ctx := t.Context()
	if err := checkSelectionMembers(ctx, t.TempDir(), nil, []string{".", "broken"}, "go1.27.1", sample); err != nil {
		t.Fatalf("a satisfied pin beside a failed sample: %v", err)
	}
	if err := checkSelectionMembers(ctx, t.TempDir(), nil, []string{".", "broken"}, "", sample); err != nil {
		t.Fatalf("no pin: %v", err)
	}
	err := checkSelectionMembers(ctx, t.TempDir(), nil, []string{"m"}, "go1.26.5", sample)
	if err == nil || !strings.Contains(err.Error(), `member "m"`) || !strings.Contains(err.Error(), `pin "go1.26.5" is not satisfied`) {
		t.Fatalf("an unsatisfied selection pin = %v", err)
	}
}

// TestTypedViewsRefuseAnUnsatisfiedSelectionPin pins the resolver
// child's typed-view arm end to end: a policy whose tagged invocation
// declares a toolchain, sampled by the seam as another release, refuses
// the load naming the member and the pin; the same policy under a
// sample equal to the pin loads (the view's own degradation then owns
// whatever the pinned toolchain cannot build) (REQ-policy-toolchain-pin).
func TestTypedViewsRefuseAnUnsatisfiedSelectionPin(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the tree")
	}
	stipulate.Covers(t, "REQ-policy-toolchain-pin")
	dir := buildSelectionModule(t)
	// The satisfied leg pins the host's own toolchain, so the view loads
	// under the local binary and no release is fetched; the unsatisfied
	// leg refuses before any load.
	local := hostGoVersion(t)
	write := func(pin string) {
		t.Helper()
		dstCfg := &stipulatorv1.GoInvocationConfig{}
		dstCfg.SetPackages([]string{"./..."})
		dstCfg.SetTags([]string{"dst"})
		dstCfg.SetToolchain(pin)
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("dst", dstCfg)})
		writePolicyRecord(t, dir, p)
	}
	orig := toolchainSampleForTest
	t.Cleanup(func() { toolchainSampleForTest = orig })
	write("go1.26.5")
	toolchainSampleForTest = func(_ context.Context, dir string, env []string) (string, error) {
		return local, nil
	}
	if _, err := newContext(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), `member "."`) || !strings.Contains(err.Error(), `pin "go1.26.5" is not satisfied`) {
		t.Fatalf("typed views under an unsatisfied selection pin = %v, want the member's pin refusal", err)
	}
	write(local)
	if _, err := newContext(context.Background(), dir, nil); err != nil {
		t.Fatalf("typed views under a satisfied selection pin: %v", err)
	}
}

// hostGoVersion is the toolchain the real go on PATH reports — the one
// pin a test can declare without fetching a release.
func hostGoVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestServedSelectionEngineRefusesAnUnsatisfiedPin pins the served
// form's arm: its selection engine samples every member through the
// operation's sampler and refuses an identified toolchain the
// selection's pin does not admit, naming the member; a satisfied pin
// builds the engine (REQ-policy-toolchain-pin).
func TestServedSelectionEngineRefusesAnUnsatisfiedPin(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the tree")
	}
	stipulate.Covers(t, "REQ-policy-toolchain-pin")
	dir := buildSelectionModule(t)
	local := hostGoVersion(t)
	sample := func(_ context.Context, dir string, env []string) (string, error) { return local, nil }
	if _, err := selectionEngine(context.Background(), dir, buildSelection{tags: []string{"dst"}, toolchain: "go1.26.5"}, sample); err == nil || !strings.Contains(err.Error(), `member "."`) || !strings.Contains(err.Error(), `pin "go1.26.5" is not satisfied`) {
		t.Fatalf("the served selection engine under an unsatisfied pin = %v, want the member's pin refusal", err)
	}
	// The satisfied leg pins the host's own toolchain: the engine builds
	// under the local binary, no release fetched.
	if _, err := selectionEngine(context.Background(), dir, buildSelection{tags: []string{"dst"}, toolchain: local}, sample); err != nil {
		t.Fatalf("the served selection engine under a satisfied pin: %v", err)
	}
}

// TestPinnableToolchainIsTheVersionGrammar pins the one predicate for a
// GOVERSION the GOTOOLCHAIN grammar can carry: a release and a vendor
// build read; a development build and an experiment-stamped version
// (which begins with "go" all the same) do not, and pin local
// (REQ-policy-toolchain-pin).
func TestPinnableToolchainIsTheVersionGrammar(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-toolchain-pin")
	for v, want := range map[string]bool{"go1.27.1": true, "go1.27.0-dst.14": true, "go1.27rc1": true, "devel go1.28-abc": false, "go1.27.0 X:nodwarf5": false, "": false} {
		if got := pinnableToolchain(v); got != want {
			t.Errorf("pinnableToolchain(%q) = %v, want %v", v, got, want)
		}
	}
}
