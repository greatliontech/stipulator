package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// failer is the slice of testing.TB and rapid.T the capture helpers
// need, so one helper serves example pins and property draws alike.
type failer interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// mustCapture is the tests' operation capture: the accepted policy
// normalized once, as every operation captures it.
func mustCapture(t failer, ctx context.Context, dir string, p *stipulatorv1.TestPolicy) *Capture {
	t.Helper()
	pc, err := CapturePolicy(ctx, dir, p)
	if err != nil {
		t.Fatal(err)
	}
	return pc
}

// raceAndPlainPolicy is the fixtures' two-invocation policy: a race leg
// and a plain leg over the whole tree — two distinct identities.
func raceAndPlainPolicy() *stipulatorv1.TestPolicy {
	race := &stipulatorv1.GoInvocationConfig{}
	race.SetPackages([]string{"./..."})
	race.SetRace(true)
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./..."})
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", race), goInvocation("plain", plain)})
	return pol
}

// spawns counts the toolchain queries an operation pays through the
// command seam, by verb: derivation costs `go env` (normalization) and
// `go list` (discovery); execution costs `go test`. Only stipulator's
// own spawns cross the seam — gofresh's engine and view loads do not —
// so this is the derivation's cost, never the operation's whole
// toolchain cost.
type spawns struct{ env, list, test, child int }

func (s spawns) plus(o spawns) spawns {
	return spawns{s.env + o.env, s.list + o.list, s.test + o.test, s.child + o.child}
}

// derivation is the spawn count with execution masked out: the
// quantity REQ-check-derivation bounds.
func (s spawns) derivation() spawns { return spawns{env: s.env, list: s.list} }

// spawnCounter is the seam counter: execution fans out per package, so
// the counts are atomic and read as a snapshot.
type spawnCounter struct{ env, list, test, child atomic.Int64 }

func (c *spawnCounter) snapshot() spawns {
	return spawns{int(c.env.Load()), int(c.list.Load()), int(c.test.Load()), int(c.child.Load())}
}

func (c *spawnCounter) reset() { c.env.Store(0); c.list.Store(0); c.test.Store(0); c.child.Store(0) }

// countSpawns installs the seam counter for the test's life.
func countSpawns(t *testing.T) *spawnCounter {
	t.Helper()
	c := &spawnCounter{}
	commandHook = func(name string, args []string) {
		if name != "go" {
			// The owned resolver child — this binary in its resolver
			// mode — the one seam spawn that is not the go tool.
			c.child.Add(1)
			return
		}
		if len(args) == 0 {
			return
		}
		switch args[0] {
		case "env":
			c.env.Add(1)
		case "list":
			c.list.Add(1)
		case "test":
			c.test.Add(1)
		}
	}
	t.Cleanup(func() { commandHook = nil })
	return c
}

// unitCosts measures, on the fixture and through the seam, what one
// derivation of the policy costs leg by leg: one normalization of every
// invocation, one discovery of every invocation, one universe. The
// pins multiply these, so they are exact counts, never bounds — and
// each leg is a positive control: a leg that spawns nothing through
// the seam would make its pin vacuous.
func unitCosts(t failer, c *spawnCounter, ctx context.Context, dir string, p *stipulatorv1.TestPolicy) (normalize, discover, universe spawns) {
	t.Helper()
	for _, inv := range p.GetInvocations() {
		n, d := invocationCosts(t, c, ctx, dir, inv)
		normalize, discover = normalize.plus(n), discover.plus(d)
	}
	c.reset()
	if _, err := discoverUniverse(ctx, dir); err != nil {
		t.Fatal(err)
	}
	universe = c.snapshot()
	if normalize.env == 0 || discover.list == 0 || universe.list == 0 {
		t.Fatalf("a derivation leg spawned nothing through the seam (normalize=%+v discover=%+v universe=%+v); its pin would be vacuous", normalize, discover, universe)
	}
	return normalize, discover, universe
}

// invocationCosts measures one invocation's normalization and discovery.
func invocationCosts(t failer, c *spawnCounter, ctx context.Context, dir string, inv *stipulatorv1.PolicyInvocation) (normalize, discover spawns) {
	t.Helper()
	c.reset()
	n, err := NormalizeInvocation(ctx, dir, inv)
	if err != nil {
		t.Fatal(err)
	}
	normalize = c.snapshot()
	c.reset()
	if _, err := DiscoverInvocation(ctx, n); err != nil {
		t.Fatal(err)
	}
	return normalize, c.snapshot()
}

// TestGoCaptureDerivesEachInvocationOnce pins REQ-check-derivation over
// a check-shaped operation: the capture pays one normalization per
// invocation, its discovered leg one discovery per invocation, the
// universe one discovery per workspace member — each on first demand
// and never again, whichever readers consult them.
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestGoCaptureDerivesEachInvocationOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	c := countSpawns(t)
	pol := raceAndPlainPolicy()
	normalize, discover, universe := unitCosts(t, c, ctx, dir, pol)

	// The operation: one capture, every reader twice.
	c.reset()
	pc := mustCapture(t, ctx, dir, pol)
	if got := c.snapshot().derivation(); got != normalize {
		t.Fatalf("capture spawned %+v, want %+v: one normalization per invocation", got, normalize)
	}
	for range 2 {
		SelectionNotices(pc)
		LiveGroupDigests(pc)
	}
	if got := c.snapshot().derivation(); got != normalize {
		t.Fatalf("notices and digests spawned %+v beyond the capture's %+v: readers of the normalized forms pay nothing", got, normalize)
	}
	for range 2 {
		if _, err := pc.discover(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := c.snapshot().derivation(), normalize.plus(discover); got != want {
		t.Fatalf("discovery spawned %+v, want %+v: one discovery per invocation, held across readers", got, want)
	}
	for range 2 {
		if _, err := pc.ObligationUniverse(ctx); err != nil {
			t.Fatal(err)
		}
	}
	total := normalize.plus(discover).plus(universe)
	if got := c.snapshot().derivation(); got != total {
		t.Fatalf("universe spawned %+v, want %+v: one universe per operation, held across readers", got, total)
	}
	// A composite reader of both legs re-derives nothing.
	if _, err := ConservationReport(ctx, pc); err != nil {
		t.Fatal(err)
	}
	if got := c.snapshot().derivation(); got != total {
		t.Fatalf("the conservation report re-derived: %+v, want %+v", got, total)
	}
}

// TestGoCaptureDerivationProperty is REQ-check-derivation's property
// witness over the derivation legs: for any policy shape (one to three
// invocations, each race or plain, tagged or not) and any sequence of
// readers, the operation pays exactly one normalization per invocation
// at capture, plus one discovery per invocation iff a reader demanded
// the discovered leg, plus one universe iff a reader demanded it —
// whatever the order and however often each reader is consulted. The
// generator's reach is stated: patterns are always "./..." over a
// populated fixture, so no draw reaches a faulting discovery — the
// held-fault pin below anchors that arm by example.
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestGoCaptureDerivationProperty(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	if testing.Short() {
		t.Skip("shells the go toolchain for every generated policy")
	}
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	c := countSpawns(t)
	// Unit costs per invocation shape, measured once: an invocation's
	// derivation cost depends on its shape (race, tags), never on its
	// name or on its neighbours, so every draw prices its invocations
	// from these four.
	type shape struct{ race, tagged bool }
	shapeOf := func(cfg *stipulatorv1.GoInvocationConfig) shape {
		return shape{cfg.GetRace(), len(cfg.GetTags()) > 0}
	}
	configure := func(sh shape) *stipulatorv1.GoInvocationConfig {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetRace(sh.race)
		if sh.tagged {
			cfg.SetTags([]string{"dup"})
		}
		return cfg
	}
	normalizeCost, discoverCost := map[shape]spawns{}, map[shape]spawns{}
	for _, sh := range []shape{{false, false}, {false, true}, {true, false}, {true, true}} {
		normalizeCost[sh], discoverCost[sh] = invocationCosts(t, c, ctx, dir, goInvocation("unit", configure(sh)))
		if normalizeCost[sh].env == 0 || discoverCost[sh].list == 0 {
			t.Fatalf("shape %+v: a derivation leg spawned nothing through the seam; its pin would be vacuous", sh)
		}
	}
	c.reset()
	if _, err := discoverUniverse(ctx, dir); err != nil {
		t.Fatal(err)
	}
	universe := c.snapshot()
	if universe.list == 0 {
		t.Fatal("the universe spawned nothing through the seam; its pin would be vacuous")
	}
	rapid.Check(t, func(rt *rapid.T) {
		count := rapid.IntRange(1, 3).Draw(rt, "invocations")
		invs := make([]*stipulatorv1.PolicyInvocation, 0, count)
		var normalize, discover spawns
		for i := range count {
			cfg := configure(shape{rapid.Bool().Draw(rt, fmt.Sprintf("race%d", i)), rapid.Bool().Draw(rt, fmt.Sprintf("tagged%d", i))})
			invs = append(invs, goInvocation(fmt.Sprintf("inv%d", i), cfg))
			normalize, discover = normalize.plus(normalizeCost[shapeOf(cfg)]), discover.plus(discoverCost[shapeOf(cfg)])
		}
		pol := &stipulatorv1.TestPolicy{}
		pol.SetInvocations(invs)
		readers := rapid.SliceOfN(rapid.SampledFrom([]string{"notices", "digests", "discover", "universe", "conservation"}), 1, 8).Draw(rt, "readers")

		c.reset()
		pc := mustCapture(rt, ctx, dir, pol)
		want := normalize
		discovered, universed := false, false
		for _, r := range readers {
			var err error
			switch r {
			case "notices":
				SelectionNotices(pc)
			case "digests":
				LiveGroupDigests(pc)
			case "discover":
				_, err = pc.discover(ctx)
				discovered = true
			case "universe":
				_, err = pc.ObligationUniverse(ctx)
				universed = true
			case "conservation":
				_, err = ConservationReport(ctx, pc)
				discovered, universed = true, true
			}
			if err != nil {
				rt.Fatal(err)
			}
		}
		if discovered {
			want = want.plus(discover)
		}
		if universed {
			want = want.plus(universe)
		}
		if got := c.snapshot().derivation(); got != want {
			rt.Fatalf("readers %v over %d invocations: derivation spawned %+v, want %+v", readers, count, got, want)
		}
	})
}

// TestGoCaptureExecutionReadersReuseTheDerivation pins the readers the
// clause names beyond the derivation legs — witness selection, the
// health-judged execution, and the outside-policy accounting — to the
// one derivation: a selective witness run, then a health-judged
// execution, then the conservation report over the same capture pay
// exactly the derivation's unit costs and nothing more.
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestGoCaptureExecutionReadersReuseTheDerivation(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	if testing.Short() {
		t.Skip("executes race and plain selective runs over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":              "module example.com/derive\n\ngo 1.26\n",
		"alpha/alpha_test.go": "package alpha\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\n",
		"beta/beta_test.go":   "package beta\n\nimport \"testing\"\n\nfunc TestBeta(t *testing.T) {}\n",
	})
	ctx := context.Background()
	c := countSpawns(t)
	pol := raceAndPlainPolicy()
	normalize, discover, universe := unitCosts(t, c, ctx, tmp, pol)
	want := normalize.plus(discover).plus(universe)

	c.reset()
	pc := mustCapture(t, ctx, tmp, pol)
	if _, err := RunWitnessesPolicy(ctx, pc, noSeeding{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExecutePolicyWitnessed(ctx, pc, noSeeding{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ConservationReport(ctx, pc); err != nil {
		t.Fatal(err)
	}
	if c.snapshot().test == 0 {
		t.Fatal("nothing executed through the seam; the pin below would prove nothing about execution readers")
	}
	if got := c.snapshot().derivation(); got != want {
		t.Fatalf("the witness run, the health-judged execution, and the conservation report spawned %+v of derivation, want the capture's %+v", got, want)
	}
}

// TestGoCapturePolicyFailsOnAnyNormalizationFault pins the clause's
// fail-closed direction: an invocation that fails to normalize fails
// the operation — no capture, an error naming the invocation — never a
// capture missing the invocation, which every reader would then treat
// as a policy that never declared it.
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestGoCapturePolicyFailsOnAnyNormalizationFault(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	good := &stipulatorv1.GoInvocationConfig{}
	good.SetPackages([]string{"./..."})
	good.SetRace(true)
	bad := &stipulatorv1.GoInvocationConfig{}
	bad.SetPackages([]string{"./..."})
	bad.SetExcludedPaths([]string{"../escape"})
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("good", good), goInvocation("bad", bad)})
	pc, err := CapturePolicy(ctx, dir, pol)
	if err == nil || pc != nil {
		t.Fatalf("capture = %v, err = %v: a faulted invocation must fail the operation, never drop out of it", pc, err)
	}
	if !strings.Contains(err.Error(), `"bad"`) {
		t.Fatalf("err = %v, want the faulted invocation named", err)
	}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("good", good)})
	if pc := mustCapture(t, ctx, dir, pol); len(pc.normalized) != 1 {
		t.Fatalf("normalized = %d, want the one accepted invocation", len(pc.normalized))
	}
}

// TestGoCaptureHoldsADiscoveryFaultForEveryReader pins the held fault:
// a discovery that faults is attributed to its invocation, and every
// later reader of the capture receives that one fault and disposes of
// it its own way — the publishing recorder degrades under it, the
// witness run errors with it.
//
// Deliberately not //gofresh:pure: discovery shells the go toolchain.
func TestGoCaptureHoldsADiscoveryFaultForEveryReader(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./nothing/..."})
	cfg.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("empty", cfg)})
	pc := mustCapture(t, ctx, dir, pol)
	c := countSpawns(t)
	r, err := NewWitnessRecorder(ctx, pc, noSeeding{})
	if err != nil {
		t.Fatalf("recorder err = %v, want a degraded recorder: a discovery fault is no abort", err)
	}
	const want = `discovering invocation "empty"`
	if !strings.Contains(r.degraded, want) {
		t.Fatalf("degraded = %q, want the fault attributed as %q", r.degraded, want)
	}
	c.reset()
	_, runErr := RunWitnessesPolicy(ctx, pc, noSeeding{})
	if runErr == nil || !strings.Contains(runErr.Error(), want) {
		t.Fatalf("run err = %v, want the held fault %q", runErr, want)
	}
	// The policy walk classed it: a selection the tree cannot honor is
	// the record's problem, the check's verdict (REQ-policy-explicit).
	if !errors.Is(runErr, policy.ErrRecord) {
		t.Fatalf("run err = %v, want a record problem", runErr)
	}
	if got := c.snapshot().derivation(); got != (spawns{}) {
		t.Fatalf("the run re-derived after the held fault: %+v", got)
	}
	// Pinned: the fault is the discovery's, so `go list` was paid for it
	// exactly once across the recorder and the run — the unit measure.
	c.reset()
	if _, err := DiscoverInvocation(ctx, pc.normalized[0]); err == nil {
		t.Fatal("the fixture's empty selection discovered without fault; the held-fault pin above proves nothing")
	}
}

// TestGoLiveGroupDigestsNameEveryPublishingCoordinate pins the store
// GC's liveness oracle to the coordinates the policy actually publishes
// under: every capture group's record identity is live, read from the
// capture's normalized forms without the discovered leg.
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestGoLiveGroupDigestsNameEveryPublishingCoordinate(t *testing.T) {
	stipulate.Covers(t, "REQ-check-derivation")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	pc := mustCapture(t, ctx, dir, raceAndPlainPolicy())
	digests := LiveGroupDigests(pc)
	if len(digests) != 2 {
		t.Fatalf("digests = %v, want one coordinate per distinct invocation identity", digests)
	}
	d, err := pc.discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range d.groups {
		if !digests[g.id] {
			t.Fatalf("group %v publishes under %s, which the live digests %v do not name", g.invs, g.id, digests)
		}
	}
}

// TestGoDiscoverRefusesAnUnmatchedPattern pins discovery's refusal of a
// selection pattern that resolves to no package: the listing's -e mode
// reports it as an entry named by the raw pattern with no directory,
// and it is a fault attributed to the invocation, never a package
// obligation whose execution would fail far from the record line.
//
// Deliberately not //gofresh:pure: discovery shells the go toolchain.
func TestGoDiscoverRefusesAnUnmatchedPattern(t *testing.T) {
	stipulate.Covers(t, "REQ-go-policy-complete")
	neutralAmbient(t)
	dir := discoverFixture(t)
	ctx := context.Background()
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./alpha", "./nothing/..."})
	n, err := NormalizeInvocation(ctx, dir, goInvocation("mixed", cfg))
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err == nil {
		t.Fatalf("obligations = %v, want a refusal naming the unmatched pattern", obs)
	}
	for _, frag := range []string{`invocation "mixed"`, `"./nothing/..."`} {
		if !strings.Contains(err.Error(), frag) {
			t.Fatalf("err = %v, want %q", err, frag)
		}
	}
	if !errors.As(err, new(unresolvedSelection)) {
		t.Fatalf("err = %v, want the selection typed unresolved: the tree lacks what the pattern names", err)
	}
	// The listing's own cause rides the refusal, folded to one line: an
	// import path no module provides says so in the toolchain's words.
	cfg.SetPackages([]string{"example.com/nowhere/absent"})
	n, err = NormalizeInvocation(ctx, dir, goInvocation("absent", cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = DiscoverInvocation(ctx, n)
	if err == nil || !strings.Contains(err.Error(), "no required module provides package") {
		t.Fatalf("err = %v, want the listing's cause", err)
	}
	if strings.ContainsAny(err.Error(), "\n\t") {
		t.Fatalf("err = %q, want one line", err)
	}
	if !errors.As(err, new(unresolvedSelection)) {
		t.Fatalf("err = %v, want the import path typed unresolved", err)
	}
	// Under vendor mode module lookup is disabled and the toolchain
	// phrases the same fact differently; the class is the same.
	vendored := writeModule(t, map[string]string{
		"go.mod":              "module example.com/vendored\n\ngo 1.26\n",
		"vendor/modules.txt":  "",
		"alpha/alpha_test.go": "package alpha\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\n",
	})
	cfg = &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"example.com/nowhere/absent"})
	cfg.SetModuleMode(stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR)
	n, err = NormalizeInvocation(ctx, vendored, goInvocation("vendored", cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = DiscoverInvocation(ctx, n)
	if err == nil || !strings.Contains(err.Error(), "cannot find module providing package") {
		t.Fatalf("err = %v, want the vendor-mode cause", err)
	}
	if !errors.As(err, new(unresolvedSelection)) {
		t.Fatalf("err = %v, want the vendor-mode import path typed unresolved", err)
	}
	// A lookup the toolchain could not perform is not evidence the tree
	// lacks the package: under module mode with the proxy off the same
	// "cannot find" phrasing opens an operational cause, and the class
	// stays operational.
	offline := writeModule(t, map[string]string{
		"go.mod":              "module example.com/offline\n\ngo 1.26\n",
		"alpha/alpha_test.go": "package alpha\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\n",
	})
	cfg = &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"example.com/nowhere/absent"})
	cfg.SetModuleMode(stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD)
	cfg.SetEnvironment([]string{"GOPROXY=off"})
	n, err = NormalizeInvocation(ctx, offline, goInvocation("offline", cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, err = DiscoverInvocation(ctx, n)
	if err == nil || !strings.Contains(err.Error(), "GOPROXY=off") {
		t.Fatalf("err = %v, want the disabled-lookup cause", err)
	}
	if errors.As(err, new(unresolvedSelection)) {
		t.Fatalf("err = %v, want an operational fault: the toolchain could not look, the tree is not shown to lack the package", err)
	}
	// The zero-entry spelling of the same fault: a directory that
	// survives with no Go files lists nothing at all, and is the
	// selection's fault exactly as an absent directory is.
	emptied := writeModule(t, map[string]string{
		"go.mod":              "module example.com/emptied\n\ngo 1.26\n",
		"tools/README":        "moved out\n",
		"alpha/alpha_test.go": "package alpha\n\nimport \"testing\"\n\nfunc TestAlpha(t *testing.T) {}\n",
	})
	cfg = &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./tools/..."})
	cfg.SetRace(true)
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("tools", cfg)})
	pc := mustCapture(t, ctx, emptied, pol)
	_, err = RunWitnessesPolicy(ctx, pc, noSeeding{})
	if err == nil || !errors.Is(err, policy.ErrRecord) {
		t.Fatalf("err = %v, want a record problem: the selection matched no packages", err)
	}
	for _, frag := range []string{`invocation "tools"`, `"./tools/..."`, "matched no packages"} {
		if !strings.Contains(err.Error(), frag) {
			t.Fatalf("err = %v, want %q", err, frag)
		}
	}
}

// TestGoResolutionFaultClassifiesByEvidence pins the classifier's
// table: the record-problem class is granted only on evidence the tree
// lacks what the pattern names, and every other cause — including an
// entry with no cause at all — stays operational.
func TestGoResolutionFaultClassifiesByEvidence(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	n := &NormalizedInvocation{Dir: discoverFixture(t)}
	entry := func(pattern, cause string) listedPackage {
		p := listedPackage{ImportPath: pattern}
		if cause != "" {
			p.Error = &listError{Err: cause}
		}
		return p
	}
	for _, tc := range []struct {
		name       string
		entry      listedPackage
		unresolved bool
	}{
		{"no cause at all", entry("./x", ""), false},
		{"no module provides it", entry("example.com/x", "no required module provides package example.com/x; to add it:\n\tgo get example.com/x"), true},
		{"vendor mode cannot find it", entry("example.com/x", "cannot find module providing package example.com/x: import lookup disabled by -mod=vendor"), true},
		{"proxy off cannot look", entry("example.com/x", "cannot find module providing package example.com/x: module lookup disabled by GOPROXY=off"), false},
		{"fetch failed cannot look", entry("example.com/x", "cannot find module providing package example.com/x: unrecognized import path \"example.com/x\": https fetch: Get \"https://example.com/x?go-get=1\": dial tcp: lookup example.com: no such host"), false},
		{"relative directory absent", entry("./vanished", "directory vanished does not exist"), true},
		{"relative tree absent", entry("./vanished/...", "pattern ./vanished/...: lstat ./vanished/: no such file or directory"), true},
		{"relative directory present but faulted", entry("./alpha", "open alpha: permission denied"), false},
	} {
		why, unresolved := resolutionFault(n, tc.entry)
		if unresolved != tc.unresolved {
			t.Errorf("%s: unresolved = %v, want %v (cause %q)", tc.name, unresolved, tc.unresolved, why)
		}
		if strings.ContainsAny(why, "\n\t") {
			t.Errorf("%s: cause %q not folded to one line", tc.name, why)
		}
	}
	// A listing that fails outright — here, an invocation directory the
	// child cannot even enter — lists nothing and says nothing about
	// the record: operational, and attributed to the invocation.
	gone := &NormalizedInvocation{Name: "gone", Dir: filepath.Join(t.TempDir(), "absent"), Packages: []string{"./..."}}
	_, err := DiscoverInvocation(context.Background(), gone)
	if err == nil || errors.As(err, new(unresolvedSelection)) || !strings.Contains(err.Error(), `invocation "gone"`) {
		t.Fatalf("err = %v, want an operational fault attributed to the invocation", err)
	}
}

// TestGoCaptureOperationalDiscoveryFaultIsNoRecordProblem pins the class
// boundary from the other side: a package directory the listing cannot
// read is an operational fault — the record is valid, the filesystem is
// not — and it must never become a verdict about the record.
//
// Deliberately not //gofresh:pure: discovery shells the go toolchain.
func TestGoCaptureOperationalDiscoveryFaultIsNoRecordProblem(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind for root")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":                     "module example.com/locked\n\ngo 1.26\n",
		"locked/locked_test.go":      "package locked\n\nimport \"testing\"\n\nfunc TestLocked(t *testing.T) {}\n",
		"locked/inner/inner_test.go": "package inner\n\nimport \"testing\"\n\nfunc TestInner(t *testing.T) {}\n",
	})
	dir := filepath.Join(tmp, "locked")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	ctx := context.Background()
	// The unreadable directory itself, and a directory beneath it whose
	// existence cannot even be checked: neither is the record's fault.
	for _, pattern := range []string{"./locked", "./locked/inner"} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{pattern})
		cfg.SetRace(true)
		pol := &stipulatorv1.TestPolicy{}
		pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
		pc := mustCapture(t, ctx, tmp, pol)
		_, err := RunWitnessesPolicy(ctx, pc, noSeeding{})
		if err == nil {
			t.Fatalf("%s: an unreadable package directory discovered without fault", pattern)
		}
		if errors.Is(err, policy.ErrRecord) {
			t.Fatalf("%s: err = %v, want an operational fault: a permission denial says nothing about the record", pattern, err)
		}
		if !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s: err = %v, want the listing's cause", pattern, err)
		}
	}
}
