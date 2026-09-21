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
	"testing"

	"github.com/greatliontech/gofresh/gotool"
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })

	var sampledDir string
	var sampledEnv []string
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		sampledDir = dir
		sampledEnv = env
		return "go99.1.0", nil
	}
	dir := t.TempDir()
	g := &captureGroup{env: append(os.Environ(), "STIPULATOR_PROVENANCE_PROBE=1")}
	if _, err := groupEngine(t.Context(), dir, g); err == nil {
		t.Fatal("groupEngine accepted an ambient toolchain a whole major ahead of the binary")
	} else if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("skew refusal = %v, want the cross-major class named", err)
	} else {
		var pe *gofresh.ToolchainProvenanceError
		if !errors.As(err, &pe) {
			t.Fatalf("refusal %v is not a *gofresh.ToolchainProvenanceError", err)
		}
	}
	if sampledDir != dir {
		t.Fatalf("sampled dir = %q, want the tree root %q", sampledDir, dir)
	}
	probed := false
	for _, kv := range sampledEnv {
		if kv == "STIPULATOR_PROVENANCE_PROBE=1" {
			probed = true
		}
	}
	if !probed {
		t.Fatal("the sample did not run under the group's environment")
	}
}

// An unidentifiable ambient toolchain refuses fail-closed, and a
// failed sample classifies identically — unidentifiable is not
// agreement.
func TestGroupEngineRefusesUnidentifiableToolchain(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "devel +abc123", nil
	}
	if _, err := groupEngine(t.Context(), t.TempDir(), &captureGroup{}); err == nil {
		t.Fatal("groupEngine accepted an unidentifiable ambient toolchain")
	} else if !strings.Contains(err.Error(), "unidentifiable") || !strings.Contains(err.Error(), "binary built with") || !strings.Contains(err.Error(), closure.AnalyzingFrontend()) {
		t.Fatalf("refusal = %v, want the composite's unidentifiable refusal naming the frontend", err)
	}

	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "", fmt.Errorf("boom")
	}
	var pe *gofresh.ToolchainProvenanceError
	if _, err := groupEngine(t.Context(), t.TempDir(), &captureGroup{}); !errors.As(err, &pe) {
		t.Fatalf("sample-failure refusal %v is not a *gofresh.ToolchainProvenanceError", err)
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })

	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "go1.1.0", nil
	}
	if err := checkToolchainProvenance(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("older-within-major ambient refused: %v", err)
	}

	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "go1.99999.0", nil
	}
	err := checkToolchainProvenance(context.Background(), t.TempDir(), nil)
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	var dirs []string
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
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
	dirs = nil
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		dirs = append(dirs, dir)
		return "", errors.New("go: no toolchain")
	}
	_, err = groupEngine(context.Background(), tmp, nested)
	if err == nil || !strings.Contains(err.Error(), "unidentifiable") {
		t.Fatalf("unsampleable group engine = %v, want the unidentifiable refusal", err)
	}
	if len(dirs) != 1 || dirs[0] != filepath.Join(tmp, "sub") {
		t.Fatalf("group engine sampled %v, want the group's module root", dirs)
	}
	// The selection arms keep the per-view rule: a member whose
	// toolchain cannot be sampled is left to its view (the default
	// view loads; binding stays healthy), and an identified skew
	// still refuses the run.
	if _, err := newContext(context.Background(), tmp, nil); err != nil {
		t.Fatalf("unsampleable member failed the binding context: %v (want the per-view degradation)", err)
	}
	if _, err := selectionEngine(context.Background(), tmp, buildSelection{}); err != nil {
		t.Fatalf("unsampleable member failed the served selection engine: %v", err)
	}
	dirs = nil
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		dirs = append(dirs, dir)
		return runtime.Version(), nil
	}
	if _, err := selectionEngine(context.Background(), tmp, buildSelection{}); err != nil {
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
	goVersionSampler = func(context.Context, string, []string) (string, error) { return "go99.1.0", nil }
	if _, err := selectionEngine(context.Background(), tmp, buildSelection{}); err == nil || !strings.Contains(err.Error(), "toolchain provenance") {
		t.Fatalf("skewed served selection engine = %v, want the skew refusal", err)
	}
}

// The provenance probe runs in the caller's own process group
// (REQ-go-owned-processes: a descendant-free query, swept with its
// caller by the owner that kills the caller outright) with the reap
// bounded (REQ-policy-cancellation), through gofresh's memoized sampler:
// two asks of one (directory, environment) spawn one `go env GOVERSION`
// (the test seam counts the prepared commands), the sample is the
// trimmed version, a cancelled operation samples nothing and the member
// walk answers a cancelled context whatever the memo holds.
func TestProvenanceProbeRunsInTheCallersGroup(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes", "REQ-policy-cancellation")
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
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(context.Context, string, []string) (string, error) { return runtime.Version(), nil }
	if err := checkSelectionMembers(context.Background(), ".", nil, []string{"."}); err != nil {
		t.Fatal(err)
	}
	if err := checkSelectionMembers(cancelled, ".", nil, []string{"."}); err == nil {
		t.Fatal("a cancelled member walk answered from the memo")
	}
}
