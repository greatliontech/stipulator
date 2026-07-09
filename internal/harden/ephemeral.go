package harden

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

// EphemeralResult is one manual mutant's evidence (REQ-harden-ephemeral):
// what was mutated, the test it ran against, whether that test killed it, and
// the attributed killer. It is finding evidence, never persisted.
type EphemeralResult struct {
	File    string `json:"file"`
	TestPkg string `json:"testPkg"`
	Run     string `json:"run"`
	Killed  bool   `json:"killed"`
	// Killer names the failing test, a timeout, or a package-scope failure
	// when Killed; empty when the mutant survived.
	Killer string `json:"killer,omitempty"`
}

// Ephemeral runs one manual mutant: it overlays file with mutant (the whole
// replacement source), runs the named test (testPkg filtered to run), and
// reports whether the test killed it — all through a build overlay, so the
// working tree is never touched (REQ-harden-ephemeral). file is tree-relative;
// testPkg is a go package path; run is a -run regex. A mutant that fails to
// compile is an error, not a survivor: nothing was measured.
func Ephemeral(ctx context.Context, dir, file string, mutant []byte, testPkg, run string, timeout time.Duration) (*EphemeralResult, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	abs, err := filepath.Abs(filepath.Join(dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	// The overlay silently no-ops if abs is not a real source file, and an
	// identical replacement measures nothing — both would read as a false
	// survivor. Resolve and compare against the original first.
	orig, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	if bytes.Equal(orig, mutant) {
		return nil, fmt.Errorf("mutant is identical to %s: nothing to measure", file)
	}

	backend, err := golang.New(dir)
	if err != nil {
		return nil, err
	}
	binFlags := rapidBinFlags(backend, testPkg)

	// The named test must actually run and pass on the unmutated tree — a
	// -run matching nothing, or an already-failing test, cannot attribute a
	// mutant, so a survivor against it would be fabricated. The baseline runs
	// with the same binFlags, so a failing rapid property never litters the
	// tree here either.
	ran, passed, err := golang.TestProbe(ctx, dir, testPkg, run, timeout, binFlags)
	if err != nil {
		return nil, err
	}
	if ran == 0 {
		return nil, fmt.Errorf("%q matched no tests in %s: nothing witnesses the mutant", run, testPkg)
	}
	if !passed {
		return nil, fmt.Errorf("the named test does not pass on the unmutated tree in %s: cannot attribute a mutant", testPkg)
	}

	m := golang.Mutant{File: abs, Source: mutant}
	outcome, killer, err := golang.RunMutant(ctx, dir, m, []string{testPkg}, run, timeout, binFlags)
	if err != nil {
		return nil, err
	}
	if outcome == golang.MutantDiscarded {
		return nil, fmt.Errorf("mutant did not compile: nothing was measured — check the replacement source for %s", file)
	}
	return &EphemeralResult{
		File:    file,
		TestPkg: testPkg,
		Run:     run,
		Killed:  outcome == golang.MutantKilled,
		Killer:  killer,
	}, nil
}

// rapidBinFlags returns the test-binary flags that keep a run from writing
// into the tree: a rapid property failure persists a reproducer under
// testdata/rapid unless -rapid.nofailfile is set. The flag is passed only to
// packages that import rapid — an unrecognized flag on a plain binary would
// read as a false kill (REQ-harden-ephemeral, "the tree never touched").
func rapidBinFlags(backend *golang.Backend, testPkg string) []string {
	if rapid, _ := backend.SplitRapidPkgs([]string{testPkg}); len(rapid) > 0 {
		return []string{"-rapid.nofailfile"}
	}
	return nil
}
