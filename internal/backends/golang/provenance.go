package golang

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
)

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
	// The class is gofresh's typed refusal; it does not survive the
	// out-of-process resolver boundary (the wire flattens errors to
	// strings) — abort semantics still hold there, the owned resolver
	// recording a sticky fault and killing the child, so a consumer
	// wanting the class on that wire must re-establish it there.
	var pe *gofresh.ToolchainProvenanceError
	if errors.As(err, &pe) {
		return true, ""
	}
	return false, err.Error()
}

// goVersionSampler samples the ambient toolchain's GOVERSION as one
// capture group resolves it — the engine's build-toolchain provenance
// half — through gofresh's memoized sampler under probeRunner: one
// sample per (directory coordinate, environment) per process, a failed
// sample memoized like an answered one, a cancelled sample never, the
// first line a cleanly exited process wrote taken when a wrapper's
// descendant holds the pipe past the wait delay. Swapped only by tests.
var goVersionSampler = (&gotool.Sampler{Runner: probeRunner}).Sample

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
	// A composite per check: the memo lives in goVersionSampler (one per
	// process, the seam tests swap), so the composite carries no state
	// worth holding.
	_, err := (&gofresh.ToolchainProvenance{Sampler: gofresh.SampleFunc(goVersionSampler)}).Check(ctx, dir, env)
	return err
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
		// The walk answers a cancelled operation whatever a sample seam
		// holds (REQ-policy-cancellation); the production sampler
		// checks the context itself.
		if err := ctx.Err(); err != nil {
			return err
		}
		ambient, err := goVersionSampler(ctx, filepath.Join(dir, m), env)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if err := gofresh.ToolchainSkew(ambient); err != nil {
			return &gofresh.ToolchainProvenanceError{Err: err}
		}
	}
	return nil
}
