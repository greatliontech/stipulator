package golang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"strings"
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

// toolchainSample samples a toolchain's GOVERSION where a directory's
// go command resolves it under an environment — gofresh's sampler
// shape.
type toolchainSample func(ctx context.Context, dir string, env []string) (string, error)

// newToolchainSample mints one judged operation's GOVERSION sampler —
// gofresh's memoized sampler under probeRunner: one sample per
// (directory coordinate, environment) for the operation's life, a
// failed sample memoized like an answered one, a cancelled sample
// never, the first line a cleanly exited process wrote taken when a
// wrapper's descendant holds the pipe past the wait delay. The memo's
// life is the operation's, never the process's (gofresh's
// toolchain-skew clause bounds a sampler to one judged run): a
// toolchain replaced under a living memo — the go on PATH swapped, a
// go.mod toolchain line moved — would otherwise be judged by its
// predecessor's sample for the process's whole life, which for the MCP
// server is every call. A resolver child's load and a served backend
// each mint their own (the engine arm samples nothing: a capture
// group's provenance is judged on the GOVERSION its invocation's
// normalization read, captureGroup.toolchain); a minted sampler asks
// the test seam first at every sample.
func newToolchainSample() toolchainSample {
	memo := &gotool.Sampler{Runner: probeRunner}
	return func(ctx context.Context, dir string, env []string) (string, error) {
		if stub := toolchainSampleForTest; stub != nil {
			return stub(ctx, dir, env)
		}
		return memo.Sample(ctx, dir, env)
	}
}

// toolchainSampleForTest, when set, is the sampler every minted sampler
// and every group's read defer to — the seam the provenance pins
// drive; nil in production.
var toolchainSampleForTest toolchainSample

// groupSample is a capture group's toolchain read for the provenance
// composite: the GOVERSION its invocation's normalization read in the
// group's module root (captureGroup.toolchain) — no spawn — or the test
// seam's answer while a pin holds one.
func groupSample(g *captureGroup) toolchainSample {
	return func(ctx context.Context, dir string, env []string) (string, error) {
		if stub := toolchainSampleForTest; stub != nil {
			return stub(ctx, dir, env)
		}
		return g.toolchain, nil
	}
}

// checkToolchainProvenance refuses the states where this binary's
// compiled-in analysis frontend cannot faithfully read what the
// group's toolchain builds (gofresh.ToolchainSkew: directional within
// a major, total across majors, unidentifiable refuses) — the guard
// every engine construction inherits through groupEngine, so no
// witness verdict is computed over a tree the binary misparses. The
// record-judging arms read it — a group's engine judges the GOVERSION
// its invocation's normalization read in the group's module root, the
// sampler handed in answering it — and an unidentifiable toolchain
// refuses there (gofresh's toolchain-skew clause); the selection-view
// arms read checkSelectionMembers, which samples each member where
// its view loads and keeps the view's own per-view degradation for a
// sample that fails.
func checkToolchainProvenance(ctx context.Context, dir string, env []string, sample toolchainSample) error {
	// A composite per check: the memo lives in the operation's sampler,
	// so the composite carries no state worth holding.
	check, err := gofresh.NewToolchainProvenance(gofresh.SampleFunc(sample))
	if err != nil {
		return err
	}
	_, err = check.Check(ctx, dir, env)
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
func checkSelectionMembers(ctx context.Context, dir string, env, members []string, pin string, sample toolchainSample) error {
	for _, m := range members {
		// The walk answers a cancelled operation whatever a sample seam
		// holds (REQ-policy-cancellation); the production sampler
		// checks the context itself.
		if err := ctx.Err(); err != nil {
			return err
		}
		ambient, err := sample(ctx, filepath.Join(dir, m), env)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if err := gofresh.ToolchainSkew(ambient); err != nil {
			return &gofresh.ToolchainProvenanceError{Err: err}
		}
		// An identified member toolchain the selection's declared pin
		// does not admit refuses the load as the skew does: the view
		// would load the selection's sources under a toolchain its
		// record never declared (REQ-policy-toolchain-pin).
		workFile := ""
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			workFile = filepath.Join(dir, "go.work")
		}
		if err := toolchainPinSatisfied(pin, ambient, toolchainRequirement(workFile, filepath.Join(dir, m))); err != nil {
			return fmt.Errorf("member %q: %w", m, err)
		}
	}
	return nil
}

// toolchainPinSatisfied is the one rule a declared toolchain pin binds
// the toolchain the environment resolves by, on every arm that reads a
// pin — the invocation's one read at normalization and a selection
// view's member samples (REQ-policy-toolchain-pin). The pin is the
// GOTOOLCHAIN grammar's, read as cmd/go reads it: "local", "path",
// "auto" and a "local+auto"/"local+path" form name no toolchain and
// require nothing; a bare "<name>" requires the resolved GOVERSION to
// equal it; "<name>+auto" and "<name>+path" select the name, or the
// module file's requirement where newer (cmd/go's selection, handed in
// as the requirement with the file it was read from) — and the
// resolved toolchain must equal that selection in Go's own version
// grammar, which reads a vendor suffix as its release
// ("go1.27.0-dst.14" is go1.27.0 to it; an exact pin distinguishes
// vendor builds, a selection never does); a side the grammar cannot
// read (a development build, an experiment-stamped version) refuses,
// naming the side, since a selection no grammar can judge is no
// selection. A refusal names the pin, the resolved toolchain, the file
// whose requirement raised the selection, and the remedy: the pinned
// toolchain installed, or the resolved one declared in the accepted
// policy (a consent-bearing edit) — for an unreadable resolved side,
// which no pin can spell, `local`.
func toolchainPinSatisfied(pin, resolved string, req moduleRequirement) error {
	switch pin {
	case "", "local", "path", "auto":
		return nil
	}
	name, bound := pin, false
	for _, suffix := range []string{"+auto", "+path"} {
		if strings.HasSuffix(pin, suffix) {
			name, bound = strings.TrimSuffix(pin, suffix), true
		}
	}
	if bound && name == "local" {
		return nil
	}
	remedy := "install the pinned toolchain, or declare the resolved one in the accepted policy"
	if !bound {
		if resolved == name {
			return nil
		}
		return fmt.Errorf("toolchain pin %q is not satisfied: the environment resolves %q — %s", pin, resolved, remedy)
	}
	if !version.IsValid(name) {
		return fmt.Errorf("toolchain pin %q names a minimum Go's version grammar cannot read — pin a release the grammar reads, or %s", pin, remedy)
	}
	if !pinnableToolchain(resolved) {
		return fmt.Errorf("toolchain pin %q cannot be judged: the environment resolves %q, which Go's version grammar cannot read and no pin can spell — pin `local`, or install a release the grammar reads", pin, resolved)
	}
	selected, by := name, "the pin"
	if req.toolchain != "" && version.IsValid(req.toolchain) && version.Compare(req.toolchain, name) > 0 {
		selected, by = req.toolchain, "the pin and "+req.file+"'s requirement"
	}
	if version.Compare(resolved, selected) != 0 {
		return fmt.Errorf("toolchain pin %q is not satisfied: %s select %q but the environment resolves %q — %s", pin, by, selected, resolved, remedy)
	}
	return nil
}

// moduleRequirement is what a module file requires of the toolchain a
// +auto/+path pin may upgrade to: the toolchain, and the file it was
// read from (named in a refusal); the zero value where no file is
// read or the lines select nothing above the pin.
type moduleRequirement struct {
	toolchain string
	file      string
}

// toolchainRequirement reads the module file as cmd/go's selection
// does (toolchain/select.go, modGoToolchain over gover.GoModLookup):
// the go.work file under a workspace (workFile non-empty), else the
// module's go.mod at modDir, its `go` and `toolchain` lines found by a
// line scan — never a parse, so a file carrying a directive this
// build's x/mod does not know still selects as the go command selects.
// A `toolchain default` line selects the pin's name alone, the go line
// ignored (cmd/go's own rule); otherwise the requirement is the larger
// of the toolchain line and the go line as a toolchain name (a bare
// language version from go1.21 on taking the ".0" release), each
// counted only where the grammar reads it.
func toolchainRequirement(workFile, modDir string) moduleRequirement {
	file := workFile
	if file == "" {
		file = filepath.Join(modDir, "go.mod")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return moduleRequirement{}
	}
	goLine, toolchainLine := goModLookup(data, "go"), goModLookup(data, "toolchain")
	if toolchainLine == "default" {
		return moduleRequirement{}
	}
	// cmd/go raises its minimum by the toolchain line, then again by the
	// go line where that is greater still: the requirement is the larger
	// of the two the grammar reads.
	required := ""
	if toolchainLine != "" && version.IsValid(toolchainLine) {
		required = toolchainLine
	}
	if goLine != "" {
		g := "go" + goLine
		if version.IsValid(g) && version.Lang(g) == g && version.Compare(g, "go1.21") >= 0 {
			g += ".0"
		}
		if version.IsValid(g) && (required == "" || version.Compare(g, required) > 0) {
			required = g
		}
	}
	if required == "" {
		return moduleRequirement{}
	}
	return moduleRequirement{toolchain: required, file: file}
}

// goModLookup is cmd/go's gover.GoModLookup: the value of the first
// line beginning with key followed by a space or tab, its trailing
// comment stripped — the scan the go command selects a toolchain by.
func goModLookup(gomod []byte, key string) string {
	for len(gomod) > 0 {
		var line []byte
		line, gomod, _ = bytes.Cut(gomod, []byte("\n"))
		line = bytes.TrimSpace(line)
		if !strings.HasPrefix(string(line), key) {
			continue
		}
		rest := strings.TrimPrefix(string(line), key)
		if len(rest) == 0 || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest, _, _ = strings.Cut(rest, "//")
		return strings.TrimSpace(rest)
	}
	return ""
}

// pinnableToolchain reports whether a resolved GOVERSION is a value the
// GOTOOLCHAIN grammar can carry — Go's version grammar reads it; a
// development build or an experiment-stamped version is not, and pins
// `local` instead; the rule's resolved side is judged by the same
// predicate.
func pinnableToolchain(resolved string) bool {
	return version.IsValid(resolved)
}
