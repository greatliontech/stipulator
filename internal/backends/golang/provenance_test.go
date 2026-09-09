package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
		var pe *toolchainProvenanceError
		if !errors.As(err, &pe) {
			t.Fatalf("refusal %v is not a *toolchainProvenanceError", err)
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
	} else if !strings.Contains(err.Error(), "unidentifiable") {
		t.Fatalf("refusal = %v, want the unidentifiable class named", err)
	}

	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "", fmt.Errorf("boom")
	}
	var pe *toolchainProvenanceError
	if _, err := groupEngine(t.Context(), t.TempDir(), &captureGroup{}); !errors.As(err, &pe) {
		t.Fatalf("sample-failure refusal %v is not a *toolchainProvenanceError", err)
	}
}

// goVersionCmd wires the tree root and the group env into the sample;
// a nil env inherits the process environment.
func TestGoVersionCmdWiresDirAndEnv(t *testing.T) {
	env := []string{"A=1", "B=2"}
	cmd := goVersionCmd(context.Background(), "/tree/root", env)
	if cmd.Dir != "/tree/root" {
		t.Fatalf("cmd.Dir = %q", cmd.Dir)
	}
	if len(cmd.Env) != 2 || cmd.Env[0] != "A=1" {
		t.Fatalf("cmd.Env = %v", cmd.Env)
	}
	if empty := goVersionCmd(context.Background(), "/tree/root", nil); empty.Env != nil {
		t.Fatalf("nil env must inherit the process environment, got %v", empty.Env)
	}
}

// The default sampler memoizes per (dir, env): one `go env` exec per
// distinct key per process, so the prerequisite's cost stays constant
// in group count.
func TestMemoizedSamplerSamplesOncePerKey(t *testing.T) {
	calls := map[string]int{}
	sampler := memoizedSampler(func(_ context.Context, dir string, env []string) (string, error) {
		calls[dir]++
		if dir == "/bad" {
			return "", fmt.Errorf("boom")
		}
		return "go1.27.0", nil
	})
	for range 3 {
		if v, err := sampler(context.Background(), "/a", []string{"K=1"}); err != nil || v != "go1.27.0" {
			t.Fatalf("sampler(/a) = %q, %v", v, err)
		}
		if _, err := sampler(context.Background(), "/bad", []string{"K=1"}); err == nil {
			t.Fatal("memoized failure did not stay a failure")
		}
	}
	if _, err := sampler(context.Background(), "/a", []string{"K=2"}); err != nil {
		t.Fatal(err)
	}
	if calls["/a"] != 2 || calls["/bad"] != 1 {
		t.Fatalf("underlying sample calls = %v, want /a:2 (two env keys), /bad:1", calls)
	}
}

// The real sampler resolves an actual GOVERSION in this repo — the
// smoke check that the exec path (command, dir, trimming) works
// outside the swapped-sampler tests.
func TestSampleGoVersionSmoke(t *testing.T) {
	v, err := sampleGoVersion(context.Background(), ".", nil)
	if err != nil {
		t.Fatalf("sampleGoVersion: %v", err)
	}
	if !strings.HasPrefix(v, "go") || strings.ContainsAny(v, " \n\t") {
		t.Fatalf("sampled GOVERSION = %q, want a trimmed go version string", v)
	}
}

// classifyFault routes exactly the provenance class to a run-level
// abort; every other fault stays a degradation reason.
func TestClassifyFaultRoutesProvenanceToAbort(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-toolchain-provenance")
	if abort, _ := classifyFault(&toolchainProvenanceError{err: fmt.Errorf("skew")}); !abort {
		t.Fatal("provenance refusal did not classify as an abort")
	}
	if abort, reason := classifyFault(fmt.Errorf("view fault")); abort || reason != "view fault" {
		t.Fatalf("ordinary fault classified abort=%v reason=%q", abort, reason)
	}
	if abort, _ := classifyFault(fmt.Errorf("wrapped: %w", &toolchainProvenanceError{err: fmt.Errorf("skew")})); !abort {
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

// The probe is bound to the operation's context — a cancelled context
// returns the cancellation, and a cancelled sample is never memoized —
// and stays in its caller's process group: a descendant-free query
// isolated in its own group would escape the sweep an owner performs
// when it kills the caller outright.
func TestProvenanceProbeRunsInTheOwnedBoundary(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes", "REQ-policy-cancellation")
	if cmd := goVersionCmd(context.Background(), ".", nil); cmd.SysProcAttr != nil || cmd.Cancel == nil {
		t.Fatalf("probe = attr %+v, cancel %v; want a context-bound command in the caller's group", cmd.SysProcAttr, cmd.Cancel != nil)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled operation is answered from the memo's head without
	// sampling; a sample the operation cancels mid-flight is never
	// memoized, so the next live operation samples again.
	calls := 0
	var cancelMidFlight context.CancelFunc
	sampler := memoizedSampler(func(ctx context.Context, dir string, env []string) (string, error) {
		calls++
		cancelMidFlight()
		return "", ctx.Err()
	})
	if _, err := sampler(cancelled, ".", nil); err == nil || calls != 0 {
		t.Fatalf("a cancelled operation sampled: calls %d, err %v", calls, err)
	}
	live, cancelLive := context.WithCancel(context.Background())
	cancelMidFlight = cancelLive
	if _, err := sampler(live, ".", nil); err == nil {
		t.Fatal("a sample cancelled mid-flight reported a version")
	}
	next, cancelNext := context.WithCancel(context.Background())
	cancelMidFlight = cancelNext
	if _, err := sampler(next, ".", nil); err == nil || calls != 2 {
		t.Fatalf("a cancelled sample was memoized: calls %d, err %v", calls, err)
	}
	if _, err := sampleGoVersion(cancelled, ".", nil); err == nil {
		t.Fatal("the probe ran under a cancelled context")
	}
	// The member walk answers a cancelled context whatever the memo
	// holds: prime the memo, then ask under cancellation.
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = memoizedSampler(func(context.Context, string, []string) (string, error) { return runtime.Version(), nil })
	if err := checkSelectionMembers(context.Background(), ".", nil, []string{"."}); err != nil {
		t.Fatal(err)
	}
	if err := checkSelectionMembers(cancelled, ".", nil, []string{"."}); err == nil {
		t.Fatal("a cancelled member walk answered from the memo")
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
