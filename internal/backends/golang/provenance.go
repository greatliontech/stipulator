package golang

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
)

// toolchainProvenanceError marks the refusal class of the engine's
// toolchain-provenance prerequisite: the invocation-level abort — a
// skewed or unidentifiable frontend would misread every package the
// run loads, so no verdict degrades group by group on it. The class
// does not survive the out-of-process resolver boundary (the wire
// flattens errors to strings); abort semantics still hold there —
// the owned resolver records a sticky fault and kills the child — so
// a future consumer wanting to distinguish this class on that wire
// must re-establish it there.
type toolchainProvenanceError struct{ err error }

func (e *toolchainProvenanceError) Error() string { return e.err.Error() }
func (e *toolchainProvenanceError) Unwrap() error { return e.err }

// classifyFault is the one boundary where a preparation fault chooses
// between the degrade path and a run-level abort: a
// toolchain-provenance refusal aborts
// (REQ-evidence-toolchain-provenance — degrading would run the full
// suite over a tree the refused frontend also discovers and selects
// from, so the degrade-to-full-execution rule never applies to it),
// while every other fault degrades to full execution
// (REQ-evidence-freshness-degrade: the cache saves work, it never
// blocks witnessing).
func classifyFault(err error) (abort bool, reason string) {
	var pe *toolchainProvenanceError
	if errors.As(err, &pe) {
		return true, ""
	}
	return false, err.Error()
}

// goVersionSampler reports the ambient toolchain's GOVERSION as one
// capture group resolves it — the engine's build-toolchain provenance
// half. Swapped only by tests. The default samples each distinct
// (dir, env) once per process: `go env` exec cost stays constant in
// group count, and within one run the sample cannot move (the tree
// and each group's environment are fixed inputs).
var goVersionSampler = memoizedSampler(sampleGoVersion)

func memoizedSampler(sample func(ctx context.Context, dir string, env []string) (string, error)) func(ctx context.Context, dir string, env []string) (string, error) {
	type result struct {
		version string
		err     error
	}
	var mu sync.Mutex
	memo := map[string]result{}
	return func(ctx context.Context, dir string, env []string) (string, error) {
		// A cancelled operation is answered with its cancellation
		// whatever the memo holds — one rule for every arm.
		if err := ctx.Err(); err != nil {
			return "", err
		}
		key := dir + "\x00" + strings.Join(env, "\x00")
		mu.Lock()
		got, ok := memo[key]
		mu.Unlock()
		if !ok {
			got.version, got.err = sample(ctx, dir, env)
			if ctx.Err() != nil {
				// A cancelled sample is no sample: never memoized.
				return "", ctx.Err()
			}
			mu.Lock()
			memo[key] = got
			mu.Unlock()
		}
		return got.version, got.err
	}
}

func sampleGoVersion(ctx context.Context, dir string, env []string) (string, error) {
	cmd := goVersionCmd(ctx, dir, env)
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		// The process exited with its answer written; a descendant a
		// wrapper left holding the pipe delayed the close, which says
		// nothing about the answer — take it rather than refuse (and
		// memoize a refusal) over a wrapper's housekeeping. The answer
		// is the first line alone: `go env GOVERSION` writes exactly
		// one, and whatever the descendant wrote after it is its own.
		answer, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		if strings.HasPrefix(answer, "go") {
			return answer, nil
		}
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("go env GOVERSION: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("go env GOVERSION: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// goVersionCmd is pure construction, split so the Dir/Env wiring is
// unit-pinnable: the sample must resolve exactly as the group's own
// loads and executions do — the target module's directory under the
// group's complete normalized environment (its GOTOOLCHAIN pin
// included, so a per-invocation declared toolchain is what gets
// judged; its owned telemetry home included, as every Go child's).
// The probe is bound to the operation's context — a cancelled
// operation kills it — but deliberately NOT through commandContext's
// group isolation: `go env` has no descendants to sweep (a toolchain
// switch replaces the process), and a spawn in its own process group
// would escape the sweep an owner performs on the caller's group when
// it kills the caller outright (the served resolver child's client
// does exactly that), so the descendant-free query stays in its
// caller's group, inside the boundary the caller's owner sweeps
// (REQ-go-owned-processes). A nil env inherits the process
// environment.
func goVersionCmd(ctx context.Context, dir string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "go", "env", "GOVERSION")
	// The kill reaches the process alone, so a descendant a shim left
	// holding the output pipe could keep the read open past it: the
	// wait is bounded, and the cancellation returns within the bound
	// (REQ-policy-cancellation is a liveness contract).
	cmd.WaitDelay = probeWaitDelay
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd
}

// checkToolchainProvenance refuses the states where this binary's
// compiled-in analysis frontend cannot faithfully read what the
// group's toolchain builds (gofresh.ToolchainSkew: directional within
// a major, total across majors, unidentifiable refuses) — the guard
// every engine construction inherits through groupEngine, so no
// witness verdict is computed over a tree the binary misparses. The
// record-judging arms read it — a group's engine samples in the
// group's module root — and an unidentifiable ambient toolchain
// refuses there (gofresh's toolchain-skew clause); the selection-view
// arms read checkSelectionMembers, which samples each member where
// its view loads and keeps the view's own per-view degradation for a
// sample that fails.
func checkToolchainProvenance(ctx context.Context, dir string, env []string) error {
	ambient, err := goVersionSampler(ctx, dir, env)
	if err != nil {
		// A failed sample leaves the ambient side unidentifiable —
		// gofresh's contract refuses that, so the sampling failure is
		// the same invocation-level class as a detected skew. The
		// message names what this side could read (the binary's own
		// build toolchain) and the failing sample.
		return &toolchainProvenanceError{err: fmt.Errorf("toolchain provenance: binary built with %s, ambient toolchain unidentifiable — refusing to judge: %w", runtime.Version(), err)}
	}
	if err := gofresh.ToolchainSkew(ambient); err != nil {
		return &toolchainProvenanceError{err: err}
	}
	return nil
}

// probeWaitDelay bounds how long a cancelled provenance probe may hold
// its caller after the kill: long enough for a real `go env` to be
// reaped, short enough that a shim's orphan cannot stall a cancelled
// operation.
const probeWaitDelay = 2 * time.Second

// checkSelectionMembers is the selection-view arms' guard, the child's
// typed views and the served form's alike: every member is sampled in
// its own directory, where its view loads (under GOTOOLCHAIN=auto the
// selected toolchain is per module, so the tree root is not a member's
// sample); an IDENTIFIED, skewed toolchain refuses the run, while a
// member whose toolchain cannot be sampled is left to its view — an
// unsampleable toolchain loads no view, and the unloadable view
// degrades to its own named per-view refusal (REQ-go-build-selections),
// so binding stays healthy for every view that does load; the served
// form's engine proceeds and resolves the member's symbols as its own
// loads allow. A cancelled operation returns its cancellation whatever
// the memo already holds.
func checkSelectionMembers(ctx context.Context, dir string, env, members []string) error {
	for _, m := range members {
		ambient, err := goVersionSampler(ctx, filepath.Join(dir, m), env)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if err := gofresh.ToolchainSkew(ambient); err != nil {
			return &toolchainProvenanceError{err: err}
		}
	}
	return nil
}
