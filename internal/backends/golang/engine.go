package golang

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/progress"
)

// beforeGroupEngineForTest, when set, runs before a group's engine is
// constructed — the seam an ordering test uses to place the engines'
// loads against the seeding backend's release.
var beforeGroupEngineForTest func()

type groupSessionState struct {
	mu      sync.Mutex
	session *gofresh.Session
	inputs  []string
	ctx     atomic.Pointer[context.Context]
}

// groupSession returns a reusable gofresh.Session for g under dir and inputs,
// preserving Session.localFacts across retry waves when pgo inputs are unchanged.
func groupSession(ctx context.Context, dir string, g *captureGroup, inputs *pgoObservation) (*gofresh.Session, error) {
	state := g.sessionState
	if state == nil {
		state = &groupSessionState{}
		g.sessionState = state
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	ctxCopy := ctx
	state.ctx.Store(&ctxCopy)
	if state.session != nil && slices.Equal(state.inputs, inputs.inputs) {
		return state.session, nil
	}
	policy := gofresh.SessionPolicy{
		EvidenceRoot:   dir,
		Vouches:        slices.Clone(g.vouches),
		Execution:      gofresh.PackageExecution,
		PrepareCommand: engineRunner.PrepareCommand,
		Progress: func(p gofresh.Progress) {
			emitEngineDiagnostic(p)
			if cp := state.ctx.Load(); cp != nil && *cp != nil {
				progress.FromContext(*cp).Keepalive()
			}
		},
	}
	if g.assumePure {
		policy.AssumePure = func(gofresh.Subject) bool { return true }
	}
	flags := append(buildFlags(g.race, g.tags, g.moduleMode, buildPGO(dir, g.pgo)), linkerBuildFlags(g.linkerAssignments)...)
	sess, err := gofresh.NewSession(ctx, gofresh.BuildDescription{
		Directory:   g.directory(dir),
		Environment: g.witnessEnv,
		Flags:       flags,
		Inputs:      append([]string{}, inputs.inputs...),
	}, policy)
	if err != nil {
		return nil, err
	}
	state.session = sess
	state.inputs = slices.Clone(inputs.inputs)
	return sess, nil
}

// groupEngine constructs a source-analysis session for dynamic-state
// explanations. Native production and checking use groupSnapshot.
func groupEngine(ctx context.Context, dir string, g *captureGroup) (*gofresh.Session, error) {
	if beforeGroupEngineForTest != nil {
		beforeGroupEngineForTest()
	}
	// Toolchain provenance is a prerequisite to constructing any
	// session: the sample is the one the invocation's normalization
	// took — `go env` in this group's module root (under
	// GOTOOLCHAIN=auto the selected toolchain is per module), whose
	// GOVERSION the group's environment then pins, so a second spawn
	// could only re-read it — judged through the backend's provenance
	// composite, so a frontend that cannot read what this group's
	// toolchain builds refuses before any verdict
	// (REQ-evidence-toolchain-provenance) — the go1.27 stale-binary
	// episode's structural fix, shared with pew and gomutant.
	if err := checkToolchainProvenance(ctx, filepath.Join(dir, filepath.FromSlash(g.moduleRoot)), g.env, groupSample(g)); err != nil {
		return nil, err
	}
	// The profile's content is build evidence no flag carries: it
	// rides the build-configuration guard as an opaque input, so a
	// profile edit at a stable path re-executes instead of serving.
	inputs, err := observePGO(ctx, dir, g)
	if err != nil {
		return nil, err
	}
	return groupSession(ctx, dir, g, inputs)
}

func (g *captureGroup) directory(root string) string {
	return filepath.Join(root, filepath.FromSlash(g.moduleRoot))
}

// newEngine is the one gofresh session constructor for source resolution: the
// analysis directory, the view's environment and build flags, the evidence
// root, and the operation's progress seam — freshness capture and validation
// are the longest silent stretches of a run, so gofresh's own analysis steps
// feed the seam as rate-limited keepalives and the engine diagnostic face.
func newEngine(ctx context.Context, dir string, env, flags []string, evidenceRoot string, inputs ...string) (*gofresh.Session, error) {
	return gofresh.NewSession(ctx, gofresh.BuildDescription{
		Directory:   dir,
		Environment: env,
		Flags:       flags,
		Inputs:      append([]string{}, inputs...),
	}, gofresh.SessionPolicy{
		EvidenceRoot: evidenceRoot,
		// The engine's own go commands ride the owned boundary
		// (REQ-go-owned-processes-runner); the loader's go list children spawn
		// through x/tools outside any runner.
		PrepareCommand: engineRunner.PrepareCommand,
		Progress: func(p gofresh.Progress) {
			emitEngineDiagnostic(p)
			progress.FromContext(ctx).Keepalive()
		},
	})
}

// engineDiagnostics receives payload-bearing gofresh diagnostics from
// every engine a derivation builds; a var so tests can pin delivery.
// The read is unsynchronized on analysis goroutines: tests may swap it
// only while no engine is live, and only engine-free tests do.
var engineDiagnostics io.Writer = os.Stderr

// engineDiagnosticSink is the one sink every engine's diagnostics
// reach — a multi-line detail folded onto one line, serialized per sink,
// built once beside the writer and never per engine; it writes through the
// swappable engineDiagnostics at call time, so a test's swap is
// honoured.
var engineDiagnosticSink = diagnosticsTo(writerFunc(func(p []byte) (int, error) { return engineDiagnostics.Write(p) }))

func progressDiagnostic(p gofresh.Progress) (string, bool) {
	detail := strings.ReplaceAll(strings.TrimSpace(p.Detail), "\n", "; ")
	if detail == "" {
		return "", false
	}
	if p.Phase == "unlisted-toolchain" {
		return "gofresh: " + detail + "\n", true
	}
	if p.Package != "" {
		return "gofresh: " + p.Phase + " " + p.Package + " — " + detail + "\n", true
	}
	return "gofresh: " + p.Phase + " — " + detail + "\n", true
}

func diagnosticsTo(w io.Writer) func(gofresh.Progress) {
	var mu sync.Mutex
	return func(p gofresh.Progress) {
		line, ok := progressDiagnostic(p)
		if !ok {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = io.WriteString(w, line)
	}
}

// emitEngineDiagnostic writes a payload-bearing gofresh event
// (per-subject analysis-unavailable provenance, the unlisted-toolchain
// notice) to the operator's log unthrottled; detail-free keep-alives
// stay silent — the keep-alive seam carries no message, and discarding
// diagnostics by signature once spent the engine's one toolchain
// announcement into a void.
func emitEngineDiagnostic(p gofresh.Progress) { engineDiagnosticSink(p) }
