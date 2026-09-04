package golang

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Witness derivation turns one in-memory execution report into the
// evidence view binding verification consumes: suite health and witness
// outcomes both derive from the same execution, never from a second run
// and never from a cache. The gating is deliberately asymmetric. A pass
// grants a witness only when its producing package disposed healthy
// inside its producing invocation and that invocation ran under the race
// detector — a red suite never yields green evidence, and a non-race
// invocation contributes suite health but no Go witness. A failure is
// surfaced regardless: red is a fact about the tree whatever the rigor of
// the run that saw it. Health is computed from invocation dispositions
// alone — the witness cache is not an input to any health or outcome here,
// so a cached green outcome structurally cannot satisfy package, command,
// or suite health. The cache appears only on the producer side: after a
// healthy race execution, per-test freshness records are published for
// later freshness-serving consumers, each carrying its own producing
// process's runtime observation and only surviving source and runtime
// producer validation.

// SuiteHealthy derives suite health from an execution report: healthy
// exactly when the report carries at least one invocation and every
// invocation's terminal disposition is healthy. Invocation dispositions
// already aggregate their packages, and they are the only health source —
// no served or cached outcome can reach this judgment.
func SuiteHealthy(report *stipulatorv1.ExecutionReport) bool {
	invocations := report.GetInvocations()
	if len(invocations) == 0 {
		return false
	}
	for _, h := range invocations {
		if h.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			return false
		}
	}
	return true
}

// invocationFacts indexes the per-invocation facts outcome gating needs:
// the race rigor of each invocation and the terminal disposition of each
// package within it.
type invocationFacts struct {
	race       map[string]bool
	plain      map[string]bool
	healthyPkg map[string]bool
}

func indexInvocations(report *stipulatorv1.ExecutionReport) invocationFacts {
	f := invocationFacts{race: map[string]bool{}, plain: map[string]bool{}, healthyPkg: map[string]bool{}}
	for _, h := range report.GetInvocations() {
		f.race[h.GetInvocation()] = h.GetGo().GetRace()
		f.plain[h.GetInvocation()] = h.GetGo().GetPlainWitness()
		for _, p := range h.GetPackages() {
			if p.GetDisposition() == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				f.healthyPkg[h.GetInvocation()+"\x00"+p.GetPackage()] = true
			}
		}
	}
	return f
}

// DeriveTestRun derives the witness-evidence view of one execution report
// for binding verification. A passing result becomes a witness outcome
// only when its producing package disposed healthy inside its producing
// invocation and that invocation ran under the race detector; a pass that
// fails either gate records no outcome at all, so a bound test in that
// position reads as unwitnessed. Failed and skipped results are recorded
// regardless — a failure is a fact whatever produced it, and a skip
// grants nothing without reading as broken. When one test name carries
// several results (an invocation with -count above one, or the same
// package under two invocations), the worst outcome wins, so a single
// red occurrence is never papered over by a green sibling. Runtime
// registrations are carried for every result — cross-checking them
// against the binding store is verification's judgment — and test-scoped
// failure diagnostics ride along so a red witness is diagnosable from the
// run that saw it.
func DeriveTestRun(report *stipulatorv1.ExecutionReport) *verify.TestRun {
	facts := indexInvocations(report)
	// Witness grants derive from witness-eligible invocations: race legs
	// at the strongest tier, explicit plain-witness admissions at the
	// plain tier with the downgrade recorded per test (PlainWitness);
	// results that never become witnesses carry no rigor claim.
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true, PlainWitness: map[string]bool{}}
	grantedRace := map[string]bool{}
	rank := func(o verify.TestOutcome) int {
		switch o {
		case verify.TestFailed:
			return 3
		case verify.TestPassed:
			return 2
		case verify.TestSkipped:
			return 1
		}
		return 0
	}
	ranTop := map[string]bool{}
	for _, row := range report.GetTests() {
		pkg, test := row.GetPackage(), row.GetTest()
		key := pkg + "." + test
		inv := row.GetProducer().GetInvocation()
		outcome := verify.TestNotRun
		switch row.GetOutcome() {
		case stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED:
			outcome = verify.TestFailed
		case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
			outcome = verify.TestSkipped
		case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
			if facts.healthyPkg[inv+"\x00"+pkg] && (facts.race[inv] || facts.plain[inv]) {
				outcome = verify.TestPassed
				if facts.race[inv] {
					grantedRace[key] = true
				}
			}
		}
		if outcome != verify.TestNotRun && rank(outcome) > rank(tr.Outcomes[key]) {
			tr.Outcomes[key] = outcome
		}
		for _, req := range row.GetRegistrations() {
			tr.Registrations = append(tr.Registrations, verify.Registration{
				Package: pkg, Test: test, Requirement: req,
			})
		}
		// Ran counts executed top-level tests and fuzz replays; examples
		// execute too but never enter the freshness cache, so counting
		// them would permanently inflate the uncacheable number. The
		// Example prefix is the toolchain's own dispatch rule, not a
		// heuristic.
		if top := topLevel(test); !strings.HasPrefix(top, "Example") {
			ranTop[pkg+"."+top] = true
		}
	}
	tr.Ran = len(ranTop)
	// A key granted by any race leg holds the race tier even when a plain
	// leg also passed it; only keys granted exclusively at the plain tier
	// carry the recorded downgrade.
	for key, outcome := range tr.Outcomes {
		if outcome == verify.TestPassed && !grantedRace[key] {
			tr.PlainWitness[key] = true
		}
	}
	for _, d := range report.GetDiagnostics() {
		if d.GetTest() == "" {
			continue
		}
		if tr.Failures == nil {
			tr.Failures = map[string]string{}
		}
		// One test can fail more than once in a run (-count above one, two
		// invocations); every occurrence's output is diagnosis material.
		key := d.GetPackage() + "." + d.GetTest()
		if prev, ok := tr.Failures[key]; ok {
			tr.Failures[key] = prev + "\n" + d.GetOutput()
		} else {
			tr.Failures[key] = d.GetOutput()
		}
	}
	sortRegs(tr)
	return tr
}

// captureGroup is one freshness-capture configuration class: every
// witness-eligible invocation whose closure-shaping configuration (build tags
// and normalized environment) is identical shares one analysis view, so
// a fingerprint is always captured under the same build selection its
// test executed under.
type captureGroup struct {
	// id is the group's stable record-identity digest over its build
	// coordinate (groupIdentity): the record-addressable form of "one
	// analysis engine declares one producer environment". Records carry
	// it, so one test's evidence under two producer environments never
	// shadows itself; groups differing only in exclusions, vouches, or
	// purity share it deliberately — their records coexist as variants
	// and each gate rides the record or fingerprint
	// (REQ-evidence-witness-cache-format).
	id   string
	tags []string
	env  []string
	// neverServes maps each of the group's subjects serving refuses to
	// its reason — random-seeded witnesses and unclassifiable subjects
	// (REQ-evidence-witness-freshness); resolved once per policy
	// capture, read by both witness forms' serving and publish paths.
	neverServes map[gofresh.Subject]string
	// witnessEnv is the group's witness environment
	// (NormalizedInvocation.WitnessEnv): revalidation recomputes env
	// digests from it as the engine's producer env; loads and analysis
	// stay on env.
	witnessEnv []string
	// race is the group's witness tier AND a build input: the analysis
	// engine's flags must describe the binary the tests actually run as,
	// and the flag rides the build-config guard into every fingerprint,
	// so records produced at one tier can never serve the other - a
	// policy flip between race and plain_witness re-executes instead of
	// laundering the tier (REQ-evidence-witness-freshness's race flag as
	// a caller-supplied build input).
	race bool
	// vouches carries the group's reviewed dynamic-state vouch set into
	// the engine; part of the group key, so one group has one set.
	vouches []string
	// assumePure carries the invocations' reviewed whole-invocation
	// purity assertion into the engine (REQ-purity-responsibility).
	assumePure bool
	// excludedPaths is the group's canonical (sorted, deduplicated)
	// reviewed observation-exclusion set. It partitions the group key —
	// two exclusion sets are two observation semantics — and every
	// record the group publishes carries it, so serving can refuse
	// evidence whose licensing exclusions the current policy has
	// withdrawn (the withdrawal re-runs; additions serve existing
	// evidence unchanged).
	excludedPaths []string
	// pkgInv names the one invocation of this group selecting each
	// package; a package two invocations select never publishes, because
	// its record would have no single producing invocation.
	pkgInv map[string]string
	// invs names the group's member invocations in policy order; the
	// explain surface reports them as the answering view's identity.
	invs []string
	// ambiguous marks packages selected by more than one invocation
	// within the group.
	ambiguous map[string]bool
	// tests holds each package's expected witness set: its named Test
	// functions and fuzz targets.
	tests map[string][]string
	// solo marks packages whose whole-package process runs exactly one
	// top-level runnable (one test or fuzz target and nothing else,
	// executable examples included in the count): only such a process can
	// carry an observation-completeness proof, because a sibling test
	// could contribute unrecorded process state to the subject's outcome.
	solo map[string]bool
	// view and fps are the pre-execution captures: fingerprints must pin
	// the tree that compiles the binaries, so capturing after execution
	// would let a mid-run edit publish pre-edit outcomes under a
	// post-edit hash — a spurious reuse. Captured before, the same
	// interleaving reads stale: the safe direction.
	view *gofresh.View
	fps  map[gofresh.Subject]gofresh.Fingerprint
	// observed carries the observation-completeness proof view for the
	// group's solo candidates, captured before execution and revalidated
	// after.
	observed    *gofresh.View
	observedFPs map[gofresh.Subject]gofresh.Fingerprint
	candidates  []gofresh.Subject
}

// WitnessRecorder is the producer side of witness freshness under the
// accepted policy: it captures per-test fingerprints before the policy
// executes and publishes witness-cache records from the execution report
// after, once source and runtime producer validation succeed. It is a
// cache producer only — health and witness evidence never depend on it,
// and any fault on this path degrades to publishing nothing while the
// derivation's evidence stands (the cache saves work, it never blocks or
// weakens witnessing).
type WitnessRecorder struct {
	dir      string
	degraded string
	groups   []*captureGroup
	// completed names the invocations whose execution finished; a
	// group publishes the moment every invocation covering one of its
	// packages has — installed at once, named on the progress stream —
	// and published marks it so Derive publishes only the remainder.
	completed map[string]bool
	published map[*captureGroup]bool
	records   []witnesscache.Record
	reasons   map[gofresh.Subject]string
}

// invocationCapture pairs one Go invocation's normalized form with its
// discovered obligation set.
type invocationCapture struct {
	n           *NormalizedInvocation
	obligations []Obligation
}

// Capture is one operation's single derivation of its accepted policy:
// every Go invocation normalized once, in record order, at construction
// — the toolchain queries normalization pays are paid exactly once per
// operation, whichever readers then consult it (REQ-check-derivation) —
// and two legs derived on first demand and held for the operation's
// life: the policy's discovery (each invocation's obligations, the
// policy-wide selection count, the witness-eligible capture groups) and
// the tree's obligation universe. Readers that need only the normalized
// forms — the selection notices, the live record-identity coordinates
// — never trigger either leg. A capture serves one operation on one
// goroutine; it is not safe for concurrent use. It performs no gofresh
// work, so the witness recorder, the selective witness runner, the
// executor, and the explainer all build on it — and they share the
// normalized forms, which discovery annotates in place (package
// directories and closure directories): every reader of the capture
// sees the one annotated form.
type Capture struct {
	dir        string
	normalized []*NormalizedInvocation
	discovered held[*policyDiscovery]
	universe   held[[]Obligation]
}

// held is a derivation performed on first demand and kept for the
// capture's life, its fault included: every later reader receives the
// same value or the same error, so no two readers can disagree.
type held[T any] struct {
	done bool
	v    T
	err  error
}

func (h *held[T]) get(derive func() (T, error)) (T, error) {
	if !h.done {
		h.done = true
		h.v, h.err = derive()
	}
	return h.v, h.err
}

// byName indexes the normalized forms by invocation name — names are
// unique in an accepted policy — for readers that route a subject to
// its covering invocation.
func (pc *Capture) byName() map[string]*NormalizedInvocation {
	out := make(map[string]*NormalizedInvocation, len(pc.normalized))
	for _, n := range pc.normalized {
		out[n.Name] = n
	}
	return out
}

// policyDiscovery is a capture's discovered leg: every invocation with
// its obligations in record order, the policy-wide package selection
// count, and the witness-eligible invocations' capture groups (sorted
// by group key).
type policyDiscovery struct {
	invocations []invocationCapture
	// globalCount counts, per package, the invocations selecting it — race
	// or not, in any group. A package selected by more than one invocation
	// can never publish or serve: its record would have no single
	// producing invocation. Counting across the whole policy keeps such
	// packages out of every capture, so their guaranteed ineligibility can
	// never strip the observation-proof leg from a group's publishable
	// candidates.
	globalCount map[string]int
	groups      []*captureGroup
	// invGroup names each witness-eligible invocation's capture group.
	invGroup map[string]*captureGroup
}

// CapturePolicy normalizes every Go invocation of the accepted policy
// once, in record order, into the operation's capture. A normalization
// fault is the operation's fault: nothing downstream can derive without
// the invocation's effective environment.
func CapturePolicy(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) (*Capture, error) {
	pc := &Capture{dir: dir}
	for _, inv := range p.GetInvocations() {
		if inv.GetGo() == nil {
			continue
		}
		n, err := NormalizeInvocation(ctx, dir, inv)
		if err != nil {
			return nil, err
		}
		pc.normalized = append(pc.normalized, n)
	}
	return pc, nil
}

// LoadCapture loads the tree's accepted policy record and captures it:
// the one entry every operation that consumes the policy takes. A
// record fault carries policy.ErrRecord for the caller's disposition;
// a normalization fault is the operation's.
func LoadCapture(ctx context.Context, dir string) (*Capture, error) {
	p, _, err := policy.Load(dir, map[string]policy.Backend{"go": Policy{}})
	if err != nil {
		return nil, err
	}
	return CapturePolicy(ctx, dir, p)
}

// ObligationUniverse is the tree's complete obligation universe —
// every workspace member's "./..." under the tree's default build
// selection — discovered on the first call and held: an operation's
// selection, execution, and outside-policy accounting all read the
// one universe. A fault, cancellation included, is held with it; ctx
// binds the first call only.
func (pc *Capture) ObligationUniverse(ctx context.Context) ([]Obligation, error) {
	return pc.universe.get(func() ([]Obligation, error) { return discoverUniverse(ctx, pc.dir) })
}

// classifySeeded resolves, over every capture group's subjects, which
// must execute every run — random-seeded witnesses
// (REQ-go-witness-class's seeded form) and unclassifiable subjects —
// and records each group's refusals on the group, the one owner both
// witness forms read: they neither serve nor publish
// (REQ-evidence-witness-freshness). One classifier call answers the
// whole policy; a classification fault is returned for the caller to
// fail closed on.
func classifySeeded(pc *policyDiscovery, seeding verify.WitnessSeeding) error {
	var symbols []string
	seen := map[string]bool{}
	for _, g := range pc.groups {
		for _, s := range groupSubjects(g) {
			if key := s.Package + "." + s.Symbol; !seen[key] {
				seen[key] = true
				symbols = append(symbols, key)
			}
		}
	}
	sort.Strings(symbols)
	refusals := map[string]string{}
	if len(symbols) > 0 {
		var err error
		if refusals, err = seeding.NeverServe(symbols); err != nil {
			return fmt.Errorf("classifying random-seeded witnesses: %w", err)
		}
	}
	for _, g := range pc.groups {
		g.neverServes = map[gofresh.Subject]string{}
		for _, s := range groupSubjects(g) {
			if why, ok := refusals[s.Package+"."+s.Symbol]; ok {
				// A refusal is attributed or it is not a refusal the
				// spec admits: an implementor answering with an empty
				// reason still refuses, under a reason that says so.
				if why == "" {
					why = "witness refused serving by the classifier without a stated reason: executes every run, never served"
				}
				g.neverServes[s] = why
			}
		}
	}
	return nil
}

// runtimeOnlyArg reports whether one extra binary argument is a
// reviewed runtime-only execution bound: witness evidence asserts a
// COMPLETED passing observation whose validity never depended on the
// bound — a bound can only prevent a measurement from completing, and
// an uncompleted measurement publishes nothing — while a subject that
// reads its remaining budget (the deadline readback) is unverifiable
// and publishes no record, so no served evidence can depend on a
// budget read. Records therefore serve
// across a bound's edits: a budget knob must be tunable without
// discarding the store. The classification fails closed: any argument
// this table cannot prove runtime-only stays identity-bearing,
// unrecognized spellings (the bare "-test.timeout" flag, the two-token
// "-test.timeout 30m" form) included. The table is consulted per
// token on the assumption that reviewed args are flag-per-token (the
// "=" single-token form): a two-token flag whose VALUE token itself
// spells "-test.timeout=..." would be misread as the reviewed bound —
// a constructible but degenerate policy; the misread widens serving,
// so keep multi-token flags out of reviewed args.
func runtimeOnlyArg(arg string) bool {
	return strings.HasPrefix(arg, "-test.timeout=")
}

// identityArgs is the identity-bearing partition of the invocation's
// extra binary arguments: everything the reviewed runtime-only table
// cannot discharge. Both the capture-group key and the record-identity
// coordinate key on this partition alone.
func identityArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if !runtimeOnlyArg(a) {
			out = append(out, a)
		}
	}
	return out
}

// groupKey is one invocation's capture-group identity: the
// closure-shaping configuration (build tags, normalized environment,
// the invocation-wide purity assertion) plus the race bit — race is a
// build input and a witness-class boundary, so a race and a
// plain-witness invocation sharing tags and env must not share a
// capture group, or one group's fingerprints would describe two
// different binaries and two witness tiers.
// keyValue is a collision-free encoded segment value. The constructors
// below are its only intended sources — none can emit a raw NUL, so a
// segment built from them can never alias the join separator or a
// neighboring segment, and handing a segment an unencoded
// caller-influenced string fails to compile instead of waiting for a
// review to notice.
type keyValue string

func quotedValue(v string) keyValue { return keyValue(fmt.Sprintf("%q", v)) }

func quotedList(vs []string) keyValue { return keyValue(quotedJoin(vs)) }

func boolValue(v bool) keyValue { return keyValue(fmt.Sprintf("%t", v)) }

// keySegment is one labeled component of a key. The segment table is
// the ONE definition tests derive their equality tuples and
// perturbation domains from — a field keyed here but absent there
// fails the tests' label-exhaustiveness check instead of going
// silently blind.
type keySegment struct {
	label string
	value keyValue
}

func encodeSegments(segments []keySegment) string {
	parts := make([]string, len(segments))
	for i, s := range segments {
		parts[i] = s.label + "=" + string(s.value)
	}
	return strings.Join(parts, "\x00")
}

// groupKeySegments is the capture-group key's canonical field table.
// The env component is the witness environment (the width cap applied;
// NormalizedInvocation.WitnessEnv): witness evidence digests under it,
// so two invocations whose delivered widths differ must not share a
// capture group. Module mode, the PGO profile, and the
// identity-bearing extra binary arguments are selection dimensions -
// two invocations differing only there build or run two different
// things and must not share one analysis view - while Count is
// repetition of the same build and the reviewed runtime-only bounds
// are execution logistics; both deliberately stay out. Every
// caller-influenced value is quoted, so no value (an env VALUE most of
// all - the one caller-arbitrary byte string here) can alias a
// separator or a neighboring segment into a colliding key.
func groupKeySegments(n *NormalizedInvocation) []keySegment {
	return []keySegment{
		{"tags", quotedList(n.Tags)},
		{"env", quotedList(witnessEnvOf(n))},
		{"modulemode", quotedValue(n.ModuleMode.String())},
		{"pgo", quotedValue(n.PGO)},
		{"args", quotedList(identityArgs(n.Args))},
		{"pure", boolValue(n.AssumePure)},
		{"race", boolValue(n.Race)},
		{"exclusions", quotedList(canonicalExclusions(n.ExcludedPaths))},
		// Vouches change verdicts, so two vouch sets are two capture
		// groups - one view never mixes two reviewed sets. Records
		// still share the identity coordinate across vouch and
		// exclusion sets: an added vouch serves existing evidence
		// unchanged, and a withdrawn vouch's records refuse in the
		// current derivation.
		{"vouches", quotedList(n.Vouches)},
	}
}

func groupKey(n *NormalizedInvocation) string {
	return encodeSegments(groupKeySegments(n))
}

// groupIdentitySegments is the record-identity coordinate's canonical
// field table (groupIdentity encodes it): the POLICY-DECLARED build
// selection and per-invocation
// semantics — tags, the race build input, the declared platform, cgo,
// GOFLAGS and toolchain pins (empty when the invocation rides the
// ambient value), workspace and module mode, the PGO profile, the
// identity-bearing extra binary arguments (runtimeOnlyArg's reviewed
// bounds re-address nothing), and the declared environment deltas
// (order-canonicalized) — and nothing more. Every ambient-resolved fact is deliberately excluded:
// the merged ambient environment, the effective toolchain, platform,
// GOFLAGS, GOEXPERIMENT, and the delivered width are the fingerprints'
// authority — an identity digesting them would silently orphan the
// whole store on a new shell, a toolchain upgrade, or a host-width
// change (and a drifted shell's prune would delete records the normal
// shell serves), while fingerprints refuse with a named reason and
// variants coexist. Exclusions, vouches, and the purity assertion
// partition capture groups (two observation semantics are two views)
// but each carries its own serving rule riding the record or the
// fingerprint (a widened exclusion set or an added vouch serves
// existing evidence unchanged; a purity flip refuses by fingerprint),
// so none of them may re-address records. Every caller-influenced
// value is quoted — set-valued and scalar alike: a value carrying the
// joiner byte or a "\x00label=" tail must not alias two coordinates,
// and quoting only the set-valued components would leave the scalar
// segments as the residual channel. Env deltas sort — duplicates are
// rejected at policy validation, so order is presentation, and a pure
// reorder must not orphan the store. The args segment carries the
// identity-bearing partition alone: a reviewed runtime-only bound
// (runtimeOnlyArg) edits without re-addressing the store.
func groupIdentitySegments(n *NormalizedInvocation) []keySegment {
	return []keySegment{
		{"tags", quotedList(n.Tags)},
		{"race", boolValue(n.Race)},
		{"goos", quotedValue(n.DeclaredGOOS)},
		{"goarch", quotedValue(n.DeclaredGOARCH)},
		{"cgo", quotedValue(n.DeclaredCgo)},
		{"goflags", quotedValue(n.DeclaredGOFLAGS)},
		{"workspace", boolValue(n.WorkspaceOn)},
		{"modulemode", quotedValue(n.ModuleMode.String())},
		{"pgo", quotedValue(n.PGO)},
		{"toolchain", quotedValue(n.DeclaredToolchain)},
		{"args", quotedList(identityArgs(n.Args))},
		{"envset", quotedList(sortedCopy(n.EnvOverrides))},
		{"envdeny", quotedList(sortedCopy(n.EnvDeny))},
	}
}

func groupIdentity(n *NormalizedInvocation) string {
	return encodeSegments(groupIdentitySegments(n))
}

// quotedJoin joins key components quoted, so a value carrying the
// joiner byte can never alias two entries into one key or coordinate.
func quotedJoin(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ",")
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// LiveGroupDigests names the record-identity coordinates the current
// policy can still address, for the store GC's cross-coordinate
// eviction: a coordinate no invocation produces is retired, and its
// records are cost no lookup will ever serve. Read from the capture's
// normalized forms alone — no discovery, no toolchain query.
func LiveGroupDigests(pc *Capture) map[string]bool {
	out := map[string]bool{}
	for _, n := range pc.normalized {
		out[groupDigest(groupIdentity(n))] = true
	}
	return out
}

// groupDigest folds a canonical identity key into its stable
// record-identity coordinate (REQ-model-hash-func's digest,
// name-economy truncated exactly as the store's file names are).
func groupDigest(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// canonicalExclusions sorts and deduplicates a reviewed exclusion set so
// group identity, record provenance, and the serving subset check all
// compare one canonical form.
func canonicalExclusions(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return slices.Compact(out)
}

// discover is the capture's discovered leg, derived on the first call
// and held: every invocation's obligations, the policy-wide selection
// count, and the witness-eligible invocations (race-enabled, plus
// explicit plain-witness admissions) folded into capture groups, each
// carrying its tier. A fault, cancellation included, is held with it;
// ctx binds the first call only.
func (pc *Capture) discover(ctx context.Context) (*policyDiscovery, error) {
	return pc.discovered.get(func() (*policyDiscovery, error) { return discoverPolicy(ctx, pc.normalized) })
}

// populatedGroups walks the capture groups that own at least one
// publishable subject, each with its subjects in deterministic order —
// the groups an engine is worth building for.
func (d *policyDiscovery) populatedGroups() iter.Seq2[*captureGroup, []gofresh.Subject] {
	return func(yield func(*captureGroup, []gofresh.Subject) bool) {
		for _, g := range d.groups {
			subjects := groupSubjects(g)
			if len(subjects) == 0 {
				continue
			}
			if !yield(g, subjects) {
				return
			}
		}
	}
}

func discoverPolicy(ctx context.Context, normalized []*NormalizedInvocation) (*policyDiscovery, error) {
	pc := &policyDiscovery{globalCount: map[string]int{}, invGroup: map[string]*captureGroup{}}
	var entries []invocationCapture
	for _, n := range normalized {
		obligations, err := DiscoverInvocation(ctx, n)
		if err != nil {
			// Attributed to the invocation: the fault reaches every
			// reader of the capture — as the recorder's degraded
			// reason, as the run's error — far from the record line
			// that named the selection. A selection the tree cannot
			// honor is the record's problem, the check's verdict
			// (REQ-policy-explicit); this walk is the record's, so the
			// class is decided here and only here.
			err = fmt.Errorf("discovering invocation %q: %w", n.Name, err)
			if errors.As(err, new(unresolvedSelection)) {
				err = policy.RecordError(err)
			}
			return nil, err
		}
		ic := invocationCapture{n: n, obligations: obligations}
		pc.invocations = append(pc.invocations, ic)
		selected := map[string]bool{}
		for _, o := range obligations {
			selected[o.Package] = true
		}
		for pkg := range selected {
			pc.globalCount[pkg]++
		}
		if n.WitnessEligible() {
			entries = append(entries, ic)
		}
	}
	byKey := map[string]*captureGroup{}
	var keys []string
	for _, e := range entries {
		n := e.n
		tests := map[string][]string{}
		runnables := map[string]int{}
		for _, o := range e.obligations {
			switch o.Kind {
			case ObligationTest, ObligationFuzz:
				tests[o.Package] = append(tests[o.Package], o.Name)
				runnables[o.Package]++
			case ObligationExample:
				runnables[o.Package]++
			}
		}
		key := groupKey(n)
		g := byKey[key]
		if g == nil {
			g = &captureGroup{
				id:            groupDigest(groupIdentity(n)),
				tags:          n.Tags,
				env:           n.Env,
				witnessEnv:    witnessEnvOf(n),
				race:          n.Race,
				assumePure:    n.AssumePure,
				vouches:       n.Vouches,
				excludedPaths: canonicalExclusions(n.ExcludedPaths),
				pkgInv:        map[string]string{},
				ambiguous:     map[string]bool{},
				tests:         map[string][]string{},
				solo:          map[string]bool{},
			}
			byKey[key] = g
			keys = append(keys, key)
		}
		if !slices.Contains(g.invs, n.Name) {
			g.invs = append(g.invs, n.Name)
		}
		pc.invGroup[n.Name] = g
		for pkg, names := range tests {
			if prev, taken := g.pkgInv[pkg]; taken && prev != n.Name {
				g.ambiguous[pkg] = true
				continue
			}
			g.pkgInv[pkg] = n.Name
			g.tests[pkg] = names
			g.solo[pkg] = runnables[pkg] == 1 && len(names) == 1
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		pc.groups = append(pc.groups, byKey[key])
	}
	return pc, nil
}

// groupSubjects enumerates one capture group's publishable subjects in
// deterministic order: every expected witness of a package exactly one
// invocation of this group selects. Selection by other groups is no
// bar — each group publishes and serves its own record under its own
// identity coordinate — only a within-group double selection (two
// same-environment invocations naming one package) has no single
// producing leg.
func groupSubjects(g *captureGroup) []gofresh.Subject {
	var subjects []gofresh.Subject
	for pkg, names := range g.tests {
		if g.ambiguous[pkg] {
			continue
		}
		for _, name := range names {
			subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: name})
		}
	}
	sort.Slice(subjects, func(i, j int) bool {
		a, b := subjects[i], subjects[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Symbol < b.Symbol
	})
	return subjects
}

// groupEngine constructs the gofresh engine for one capture group's
// closure-shaping configuration.
func groupEngine(ctx context.Context, dir string, g *captureGroup) (*gofresh.Engine, error) {
	// Toolchain provenance is a prerequisite to constructing any
	// engine: the sample resolves as this group's own loads do (the
	// tree root under the group's normalized environment, its
	// GOTOOLCHAIN pin included), so a frontend that cannot read what
	// this group's toolchain builds refuses before any verdict —
	// the go1.27 stale-binary episode's structural fix, shared with
	// pew and gomutant.
	if err := checkToolchainProvenance(dir, g.env); err != nil {
		return nil, err
	}
	opts := []gofresh.Option{gofresh.WithProducerEnv(g.witnessEnv...)}
	if g.assumePure {
		// The reviewed purity assumption rides every subject of the
		// group as a caller assertion; the recorded fingerprints carry
		// caller-assertion attribution on every record; an explicit
		// gofresh:external declaration is never suppressed by it.
		opts = append(opts, gofresh.WithAssumePure(func(gofresh.Subject) bool { return true }))
	}
	if len(g.vouches) > 0 {
		// The reviewed dynamic-state vouch set: discharging vouches ride
		// every record's evidence, so acceptance is auditable there.
		opts = append(opts, gofresh.WithDynamicStateVouches(g.vouches...))
	}
	// Every consumer of these views' verdicts follows the
	// producer-view discipline: each group's served outcomes AND its
	// published records stand only after the group's ONE closing
	// validation (publishEligible's closeGroup, last on the view),
	// so checks defer their closing base observation to that single
	// validation instead of paying a full re-observation per call
	// (gofresh's deferred-close contract).
	opts = append(opts, gofresh.WithDeferredCheckClose())
	return newEngine(ctx, dir, g.env, selectionBuildFlags(g.race, g.tags), opts...)
}

// newEngine is the one gofresh engine constructor: the tree root, the
// view's environment and build flags, and the operation's progress
// seam — freshness capture and validation are the longest silent
// stretches of a run, so gofresh's own analysis steps feed the seam as
// rate-limited keepalives and the engine diagnostic face. Callers add
// their role's options.
func newEngine(ctx context.Context, dir string, env, flags []string, extra ...gofresh.Option) (*gofresh.Engine, error) {
	opts := []gofresh.Option{
		gofresh.WithDir(dir),
		gofresh.WithBuildFlags(flags...),
		gofresh.WithEnv(env...),
		gofresh.WithProgress(func(p gofresh.Progress) {
			emitEngineDiagnostic(p)
			progress.FromContext(ctx).Keepalive()
		}),
	}
	return gofresh.New(append(opts, extra...)...)
}

// engineDiagnostics receives payload-bearing gofresh diagnostics from
// every engine a derivation builds; a var so tests can pin delivery.
// The read is unsynchronized on analysis goroutines: tests may swap it
// only while no engine is live, and only engine-free tests do.
var engineDiagnostics io.Writer = os.Stderr

// emitEngineDiagnostic writes a payload-bearing gofresh event
// (per-subject analysis-unavailable provenance, the unlisted-toolchain
// notice) to the operator's log unthrottled; detail-free keep-alives
// stay silent — the keep-alive seam carries no message, and discarding
// diagnostics by signature once spent the engine's one toolchain
// announcement into a void.
func emitEngineDiagnostic(p gofresh.Progress) {
	if p.Detail != "" {
		fmt.Fprintf(engineDiagnostics, "gofresh: %s %s — %s\n", p.Phase, p.Package, p.Detail)
	}
}

// NewWitnessRecorder prepares freshness publication for one execution of
// the accepted policy: it must be called before the policy executes, so
// the captured fingerprints pin the tree the execution compiles. Only
// race-enabled Go invocations are captured — a non-race invocation grants
// no witness evidence, so nothing it produces may enter the cache a
// freshness-serving run would grant evidence from. A fault while
// preparing disables publication and is reported through the derived
// run's degraded reason, never as an error — publication is
// optimization, not correctness — with one exception: a
// toolchain-provenance refusal returns as the error, a run-level
// abort (REQ-evidence-toolchain-provenance; classifyFault), because
// the refused frontend also discovered and selected the suite the
// degraded run would execute.
func NewWitnessRecorder(ctx context.Context, pc *Capture, seeding verify.WitnessSeeding) (*WitnessRecorder, error) {
	dir := pc.dir
	r := &WitnessRecorder{dir: dir, completed: map[string]bool{}, published: map[*captureGroup]bool{}, reasons: map[gofresh.Subject]string{}}
	degrade := func(err error) (*WitnessRecorder, error) {
		abort, reason := classifyFault(err)
		if abort {
			return nil, err
		}
		r.degraded = reason
		r.groups = nil
		return r, nil
	}
	d, err := pc.discover(ctx)
	if err != nil {
		return degrade(err)
	}
	// A seeding-classification fault degrades the run exactly as an
	// engine fault does: nothing publishes, so nothing can later serve
	// a witness the fault left unclassified (fail closed).
	if err := classifySeeded(d, seeding); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degraded = err.Error()
		return r, nil
	}
	for g, subjects := range d.populatedGroups() {
		engine, err := groupEngine(ctx, dir, g)
		if err != nil {
			return degrade(err)
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			return degrade(err)
		}
		g.view = view
		g.fps = map[gofresh.Subject]gofresh.Fingerprint{}
		for _, s := range subjects {
			// A random-seeded witness never publishes, so it is never
			// fingerprinted; any other subject that fails to fingerprint
			// simply stays unpublishable. Execution and evidence are
			// untouched either way.
			if _, refused := g.neverServes[s]; refused {
				continue
			}
			if fp, err := view.Capture(ctx, s); err == nil {
				g.fps[s] = fp
			}
		}
		for _, s := range subjects {
			fp, captured := g.fps[s]
			if captured && g.solo[s.Package] && fp.PurityAssertion == "" {
				g.candidates = append(g.candidates, s)
			}
		}
		g.observed, g.observedFPs = observedView(ctx, g.view, g.candidates)
		r.groups = append(r.groups, g)
	}
	// Release transient package-loading memory before the caller spawns
	// race-instrumented builds; the views stay alive for post-execution
	// producer validation.
	debug.FreeOSMemory()
	return r, nil
}

// producerKey identifies one producing process for observation lookup.
type producerKey struct {
	invocation string
	pid        int64
	ordinal    int32
}

func keyOfProducer(p *stipulatorv1.ProducerIdentity) producerKey {
	return producerKey{invocation: p.GetInvocation(), pid: p.GetProcessId(), ordinal: p.GetProcessOrdinal()}
}

// Derive turns one execution report into the run's witness-evidence view
// and publishes per-test freshness records from it. Evidence and health
// come from DeriveTestRun alone; publication then covers exactly the
// tests whose producing package disposed healthy inside a race-enabled
// invocation and whose producing process owns a completed observation —
// that process's own observation and never a sibling's, because an
// observation proves only what its own process read. Records survive to
// the cache only after the analysis views revalidate (source producer
// validation) and each fingerprint's post-run check returns valid
// (runtime producer validation); a record whose inputs moved during
// execution, or whose observation is unverifiable, is dropped and counted
// uncacheable rather than published. The error return is reserved for
// caller cancellation.
func (r *WitnessRecorder) Derive(ctx context.Context, report *stipulatorv1.ExecutionReport, observations []*ProcessObservation) (*verify.TestRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tr := DeriveTestRun(report)
	published, uncacheableWhy, degraded := r.publish()
	executedTop := executedTopKeys(report)
	switch {
	case degraded != "":
		// The fault ended publication; what earlier groups installed
		// stays — each validated by its own closing check — and only
		// the rest reads uncacheable, so the run tells one story with
		// the store and its ending.
		tr.Degraded = degraded
		installed := map[string]bool{}
		for _, rec := range published {
			installed[rec.Package+"."+rec.Test] = true
		}
		// A subject the ladder refused before the fault keeps the
		// ladder's own reason; the degrade explains only the rest.
		tr.Uncached = 0
		tr.UncacheableReasons = map[string]string{}
		for s, why := range uncacheableWhy {
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why
		}
		for key := range executedTop {
			if installed[key] {
				delete(tr.UncacheableReasons, key)
				continue
			}
			tr.Uncached++
			if _, ok := tr.UncacheableReasons[key]; !ok {
				tr.UncacheableReasons[key] = "freshness path degraded: " + degraded
			}
		}
	case len(r.groups) == 0:
		// Nothing was capturable (no witness-eligible invocation, or no
		// expected tests): every executed test is uncacheable and the
		// existing cache is left alone.
		tr.Uncached = tr.Ran
		tr.UncacheableReasons = map[string]string{}
		for key := range executedTop {
			tr.UncacheableReasons[key] = "no capture group: no witness-eligible invocation covers the package"
		}
	default:
		// Published is a record count (one per group); Uncached is a
		// subject count in Ran's unit, so distinct published subjects
		// are the subtrahend — two group records for one shared package
		// must not mask another subject's drop.
		publishedSubjects := map[string]bool{}
		for _, rec := range published {
			publishedSubjects[rec.Package+"."+rec.Test] = true
		}
		if tr.Ran > len(publishedSubjects) {
			tr.Uncached = tr.Ran - len(publishedSubjects)
		}
		// Per-test attribution mirrors the selective runner's: the
		// ladder's own refusal reasons plus a structural fallback
		// (REQ-evidence-witness-freshness's diagnosable-set requirement).
		if tr.Uncached > 0 || len(uncacheableWhy) > 0 {
			tr.UncacheableReasons = map[string]string{}
			for s, why := range uncacheableWhy {
				tr.UncacheableReasons[s.Package+"."+s.Symbol] = why
			}
			publishedKey := map[string]bool{}
			for _, rec := range published {
				publishedKey[rec.Package+"."+rec.Test] = true
			}
			for key := range executedTop {
				if publishedKey[key] {
					delete(tr.UncacheableReasons, key)
					continue
				}
				if _, ok := tr.UncacheableReasons[key]; !ok {
					tr.UncacheableReasons[key] = "record not published"
				}
			}
		}
		// Publication installed at production: each group's records
		// landed as their own variant files the moment its last covering
		// invocation completed. Records this
		// run never touched — a shadowed sibling's, a package this policy
		// never selected's — need no rewrite: the store is per-record, so
		// retention is the default and nothing shrinks. A departed test's
		// variants linger as dead weight — its identity never installs
		// again, so no bound fires; store growth is cost, never
		// correctness.
	}
	return tr, nil
}

// covered reports whether every invocation covering one of g's
// packages has completed — the moment g's records can publish.
func (r *WitnessRecorder) covered(g *captureGroup) bool {
	for pkg := range g.tests {
		if g.ambiguous[pkg] {
			continue
		}
		if inv, ok := g.pkgInv[pkg]; ok && !r.completed[inv] {
			return false
		}
	}
	return true
}

// invocationCompleted is the completion hook of the health-judged
// form: every group whose covering invocations have all completed
// publishes now, from the report so far, and installs at once — the
// unit of persistence is the witness group at its last covering
// invocation's completion on this form as on the selective one
// (REQ-policy-cancellation), named on the progress stream by the
// completing invocation. A publication fault degrades the run whole,
// as at the end; the error return is reserved for caller cancellation.
func (r *WitnessRecorder) invocationCompleted(ctx context.Context, invocation string, sofar *stipulatorv1.ExecutionReport, observations []*ProcessObservation) error {
	r.completed[invocation] = true
	if r.degraded != "" {
		return nil
	}
	installed, degraded, err := r.publishRemaining(ctx, sofar, observations, r.covered)
	// What landed is named before any error returns: a cancellation
	// between two groups' installs must not leave records on disk the
	// ending never mentions.
	if installed > 0 {
		progress.FromContext(ctx).Persisted(invocation, installed)
	}
	if err != nil {
		return err
	}
	if degraded != "" {
		r.degraded = degraded
	}
	return nil
}

// publishRemaining assembles, validates, and installs the freshness
// records the report supports for every group not yet published that
// ready admits — a group is published once its records have been
// offered to the store, and only the records that landed enter the
// account, so the recorder's account and the store never disagree. It
// returns the count installed, and the degraded reason when a fault
// disabled further publication — records installed before the fault
// stay, each validated by its own group's closing check; the error
// return is reserved for caller cancellation.
func (r *WitnessRecorder) publishRemaining(ctx context.Context, report *stipulatorv1.ExecutionReport, observations []*ProcessObservation, ready func(*captureGroup) bool) (int, string, error) {
	if r.degraded != "" {
		return 0, r.degraded, nil
	}
	facts := indexInvocations(report)
	rowsByInvPkg := map[string][]*stipulatorv1.TestResult{}
	for _, row := range report.GetTests() {
		k := row.GetProducer().GetInvocation() + "\x00" + row.GetPackage()
		rowsByInvPkg[k] = append(rowsByInvPkg[k], row)
	}
	obsByProducer := map[producerKey]*ProcessObservation{}
	for _, o := range observations {
		obsByProducer[keyOfProducer(o.Wire.GetProducer())] = o
	}
	installed := 0
	for _, g := range r.groups {
		if r.published[g] || !ready(g) {
			continue
		}
		records, reasons, degraded, err := r.publishGroup(ctx, g, facts, rowsByInvPkg, obsByProducer)
		if err != nil || degraded != "" {
			return installed, degraded, err
		}
		maps.Copy(r.reasons, reasons)
		for _, rec := range records {
			if err := witnesscache.Install(r.dir, rec); err != nil {
				// The store's fault, named as such: the evidence
				// qualified, the write did not land.
				r.reasons[gofresh.Subject{Package: rec.Package, Symbol: rec.Test}] = "the store refused the record: " + err.Error()
				continue
			}
			r.records = append(r.records, rec)
			installed++
		}
		r.published[g] = true
	}
	return installed, "", nil
}

// publish is the run's publication account: every record the
// completion hook installed and every refusal reason, with the degraded
// reason when a later group's fault ended publication. Every group is
// covered by the time the last invocation completes — groups derive
// from the invocations the executor walks — so nothing is left to
// publish here.
func (r *WitnessRecorder) publish() ([]witnesscache.Record, map[gofresh.Subject]string, string) {
	return append([]witnesscache.Record(nil), r.records...), maps.Clone(r.reasons), r.degraded
}

// groupSubject is one publishable subject's execution-side material.
type groupSubject struct {
	subject  gofresh.Subject
	obs      *ProcessObservation
	rows     []*stipulatorv1.TestResult
	soloRun  bool
	outcomes map[string]string
	regs     []verify.Registration
}

func (r *WitnessRecorder) publishGroup(ctx context.Context, g *captureGroup, facts invocationFacts, rowsByInvPkg map[string][]*stipulatorv1.TestResult, obsByProducer map[producerKey]*ProcessObservation) ([]witnesscache.Record, map[gofresh.Subject]string, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, "", err
	}
	reasons := map[gofresh.Subject]string{}

	eligible := map[gofresh.Subject]*groupSubject{}
	var order []gofresh.Subject
	for pkg, names := range g.tests {
		markAll := func(why string) {
			for _, name := range names {
				reasons[gofresh.Subject{Package: pkg, Symbol: name}] = why
			}
		}
		inv, ok := g.pkgInv[pkg]
		if !ok || g.ambiguous[pkg] {
			markAll("two invocations of one capture group select the package; no single producing leg")
			continue
		}
		if !facts.healthyPkg[inv+"\x00"+pkg] {
			markAll("producing package disposed unhealthy")
			continue
		}
		rows := rowsByInvPkg[inv+"\x00"+pkg]
		if len(rows) == 0 {
			markAll("no terminal event from the producing process")
			continue
		}
		// The executor launches exactly one process per selected package per
		// invocation, so every row under this key shares one producer.
		producer := keyOfProducer(rows[0].GetProducer())
		obs := obsByProducer[producer]
		if obs == nil || obs.Wire.GetCompleted() == nil {
			// The producing process's testlog flush is unproven: its
			// tests execute and witness, they just cannot cache.
			markAll("producing process's testlog flush unproven")
			continue
		}
		tops := map[string]bool{}
		for _, row := range rows {
			tops[topLevel(row.GetTest())] = true
		}
		for _, name := range names {
			subject := gofresh.Subject{Package: pkg, Symbol: name}
			if why, refused := g.neverServes[subject]; refused {
				reasons[subject] = why
				continue
			}
			if _, captured := g.fps[subject]; !captured {
				reasons[subject] = "pre-execution fingerprint capture failed"
				continue
			}
			gs := &groupSubject{subject: subject, obs: obs, soloRun: len(tops) == 1 && tops[name]}
			gs.outcomes = map[string]string{}
			contradicted := false
			for _, row := range rows {
				test := row.GetTest()
				if test != name && !strings.HasPrefix(test, name+"/") {
					continue
				}
				var word string
				switch row.GetOutcome() {
				case stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED:
					word = "passed"
				case stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED:
					word = "skipped"
				default:
					// A failed result inside a healthy package is a
					// contradiction; refuse the record rather than cache
					// either side of it.
					contradicted = true
				}
				gs.outcomes[row.GetPackage()+"."+test] = word
				for _, req := range row.GetRegistrations() {
					gs.regs = append(gs.regs, verify.Registration{Package: pkg, Test: test, Requirement: req})
				}
			}
			if contradicted || gs.outcomes[pkg+"."+name] == "" {
				reasons[subject] = "no healthy outcome for the subject"
				continue
			}
			eligible[subject] = gs
			order = append(order, subject)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Symbol < b.Symbol
	})

	// The shared publication ladder (publishEligible) takes over from
	// eligibility: proof leg, final fingerprints, post-run check, the
	// one closing validation, record assembly. This full-execution path
	// has no served outcomes; a check fault or closing refusal degrades
	// the run's further publication with the named cause rather than
	// filling per-subject reasons — this group's evidence executed
	// under one view a tree edit disproved wholesale; groups that
	// closed before it keep what their own views validated.
	eligibleSubjects := map[gofresh.Subject]*pubSubject{}
	for s, gs := range eligible {
		eligibleSubjects[s] = &pubSubject{obs: gs.obs, outcomes: gs.outcomes, regs: gs.regs, solo: gs.soloRun}
	}
	records, _, checkFault, closeFault, fatal := publishEligible(ctx, g.id, g.view, g.observed, g.observedFPs, g.candidates, order, eligibleSubjects, g.fps, g.excludedPaths, nil, nil, reasons)
	if fatal != nil {
		return nil, nil, "", fatal
	}
	if checkFault != nil {
		return nil, nil, fmt.Sprintf("runtime producer validation failed: %v", checkFault), nil
	}
	if closeFault != nil {
		return nil, nil, fmt.Sprintf("source producer validation failed: %v", closeFault), nil
	}
	return records, reasons, "", nil
}

func compactRegs(regs []verify.Registration) []verify.Registration {
	out := regs[:0]
	for i, reg := range regs {
		if i == 0 || reg != regs[i-1] {
			out = append(out, reg)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ExecutePolicyWitnessed executes the accepted policy once and derives
// suite health and witness evidence from that same execution
// (SuiteHealthy over the report, and the returned test run): freshness
// fingerprints are captured before execution so published records pin the
// tree that compiled the binaries, and per-test records publish only
// after source and runtime producer validation. Caller cancellation
// anywhere discards the whole result.
func ExecutePolicyWitnessed(ctx context.Context, pc *Capture, seeding verify.WitnessSeeding) (*stipulatorv1.ExecutionReport, *verify.TestRun, error) {
	rep := progress.FromContext(ctx)
	// The pre-execution recorder derives the capture's discovered leg:
	// discovery-phase work.
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	recorder, err := NewWitnessRecorder(ctx, pc, seeding)
	if err != nil {
		// A toolchain-provenance refusal aborts before the suite runs:
		// the refused frontend discovered and selected it
		// (REQ-evidence-toolchain-provenance).
		return nil, nil, err
	}
	report, observations, err := executePolicy(ctx, pc, func(invocation string, sofar *stipulatorv1.ExecutionReport, observations []*ProcessObservation) error {
		return recorder.invocationCompleted(ctx, invocation, sofar, observations)
	})
	if err != nil {
		return nil, nil, err
	}
	// Producer validation and publication judge the evidence the run
	// produced: verification-phase work.
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	tr, err := recorder.Derive(ctx, report, observations)
	if err != nil {
		return nil, nil, err
	}
	// The witness-eligible selection boundary holds on this form too: a
	// subject whose every executing invocation is ineligible can never be
	// granted a witness outcome, and an expected witness no invocation
	// executed cannot either - both are outside, counted and marked
	// exactly as the selective form counts them
	// (REQ-check-witness-selection). The universe is the one the
	// execution itself selected against — the capture's, discovered
	// before the suite ran: a test the run could only have created
	// mid-execution predates no policy, and a source edit mid-run
	// discards the run's records anyway. A universe fault degrades
	// silently: without it only executed subjects classify.
	facts := indexInvocations(report)
	eligibleCovered := map[string]bool{}
	executed := map[string]bool{}
	for _, row := range report.GetTests() {
		top := topLevel(row.GetTest())
		if strings.HasPrefix(top, "Example") {
			continue
		}
		key := row.GetPackage() + "." + top
		executed[key] = true
		inv := row.GetProducer().GetInvocation()
		if facts.race[inv] || facts.plain[inv] {
			eligibleCovered[key] = true
		}
	}
	outsideSubjects := map[string]bool{}
	for key := range executed {
		if !eligibleCovered[key] {
			outsideSubjects[key] = true
		}
	}
	if universe, uerr := pc.ObligationUniverse(ctx); uerr == nil {
		for _, o := range universe {
			if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
				continue
			}
			key := o.Package + "." + o.Name
			if !eligibleCovered[key] {
				outsideSubjects[key] = true
			}
		}
	}
	tr.OutsideSubjects = outsideSubjects
	tr.OutsidePolicy = len(outsideSubjects)
	return report, tr, nil
}

// executedTopKeys is the executed top-level witness-subject key set —
// "pkg.TopLevelTest" per report row, examples excluded by the
// toolchain's own dispatch rule, the same rule Ran counts by: the
// attribution map and the Ran count must never desynchronize.
func executedTopKeys(report *stipulatorv1.ExecutionReport) map[string]bool {
	keys := map[string]bool{}
	for _, row := range report.GetTests() {
		if top := topLevel(row.GetTest()); !strings.HasPrefix(top, "Example") {
			keys[row.GetPackage()+"."+top] = true
		}
	}
	return keys
}
