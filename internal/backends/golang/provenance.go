package golang

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

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

func memoizedSampler(sample func(dir string, env []string) (string, error)) func(dir string, env []string) (string, error) {
	type result struct {
		version string
		err     error
	}
	var mu sync.Mutex
	memo := map[string]result{}
	return func(dir string, env []string) (string, error) {
		key := dir + "\x00" + strings.Join(env, "\x00")
		mu.Lock()
		got, ok := memo[key]
		mu.Unlock()
		if !ok {
			got.version, got.err = sample(dir, env)
			mu.Lock()
			memo[key] = got
			mu.Unlock()
		}
		return got.version, got.err
	}
}

func sampleGoVersion(dir string, env []string) (string, error) {
	cmd := goVersionCmd(dir, env)
	out, err := cmd.Output()
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
// loads and executions do — the tree root under the group's complete
// normalized environment (its GOTOOLCHAIN pin included, so a
// per-invocation declared toolchain is what gets judged). A nil env
// inherits the process environment.
func goVersionCmd(dir string, env []string) *exec.Cmd {
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd
}

// checkToolchainSkewIdentified is the selection-view arm's guard: an
// IDENTIFIED, skewed toolchain refuses, while a failed sample returns
// nil — on that arm an unsampleable toolchain cannot load a view at
// all, and the unloadable view degrades to its own named per-view
// refusal (REQ-go-build-selections) instead of failing the whole
// binding context. The engine arm keeps the stricter rule: its env
// was normalized from a working toolchain, so a failed sample there
// is anomalous and refuses (checkToolchainProvenance).
func checkToolchainSkewIdentified(dir string, env []string) error {
	ambient, err := goVersionSampler(dir, env)
	if err != nil {
		return nil
	}
	if err := gofresh.ToolchainSkew(ambient); err != nil {
		return &toolchainProvenanceError{err: err}
	}
	return nil
}

// checkToolchainProvenance refuses the states where this binary's
// compiled-in analysis frontend cannot faithfully read what the
// group's toolchain builds (gofresh.ToolchainSkew: directional within
// a major, total across majors, unidentifiable refuses) — the guard
// every engine construction inherits through groupEngine, so no
// witness verdict is computed over a tree the binary misparses.
func checkToolchainProvenance(dir string, env []string) error {
	ambient, err := goVersionSampler(dir, env)
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
