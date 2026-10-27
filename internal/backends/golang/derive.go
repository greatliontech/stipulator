package golang

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// Witness derivation turns one in-memory execution report into the
// evidence view binding verification consumes: suite health and witness
// outcomes both derive from the same execution, never from a second run
// and never from a cache. The gating is deliberately asymmetric. A pass
// grants a witness only when its producing package disposed healthy
// inside a race-enabled or explicitly plain-admitted invocation. A red suite
// never yields green evidence; an unadmitted non-race invocation contributes
// suite health but no Go witness. A failure is
// surfaced regardless: red is a fact about the tree whatever the rigor of
// the run that saw it. Health is computed from invocation dispositions
// alone — the witness cache is not an input to any health or outcome here,
// so a cached green outcome structurally cannot satisfy package, command,
// or suite health. The cache appears only on the producer side: after a
// healthy witness-eligible execution, per-test freshness records are published for
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
				f.healthyPkg[invPkgKey(h.GetInvocation(), p.GetPackage())] = true
			}
		}
	}
	return f
}

// DeriveTestRun derives the witness-evidence view of one execution report
// for binding verification. A passing result becomes a witness outcome
// only when its producing package disposed healthy inside its producing
// invocation and that invocation ran under the race detector or explicitly
// admitted plain witnesses; a pass that fails either gate records no outcome
// at all, so a bound test in that position reads as unwitnessed. Failed and
// skipped results are recorded
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
			if facts.healthyPkg[invPkgKey(inv, pkg)] && (facts.race[inv] || facts.plain[inv]) {
				outcome = verify.TestPassed
				if facts.race[inv] {
					grantedRace[key] = true
				}
			}
		}
		if outcome != verify.TestNotRun && verify.OutcomeRank(outcome) > verify.OutcomeRank(tr.Outcomes[key]) {
			tr.Outcomes[key] = outcome
		}
		for _, req := range row.GetRegistrations() {
			tr.Registrations = append(tr.Registrations, verify.Registration{
				Package: pkg, Test: test, Requirement: req,
			})
		}
		// Ran counts executed top-level tests and fuzz replays, never
		// examples.
		if key, ok := executedTopKey(pkg, test); ok {
			ranTop[key] = true
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
	tr.Timing = report.GetTiming()
	return tr
}

// groupPackage retains the policy-discovered witness subjects and their selecting
// invocation. Current runnable populations belong to witnessSnapshot instead.
// A package selected twice in one capture group has no producing leg.
type groupPackage struct {
	inv       string
	names     []string
	ambiguous bool
}

// selectingInvocation names the one invocation of the group selecting
// the package, or nothing where the group holds no such package. A
// caller walking every group for a selection's package reads the empty
// answer as "not this group"; a caller asking for one of the group's
// own subjects has an entry by construction and reads the name
// directly.
func (g *captureGroup) selectingInvocation(pkg string) string {
	if p := g.packages[pkg]; p != nil {
		return p.inv
	}
	return ""
}

// subjectsOf lists a selection's subjects ordered by package, then
// symbol.
func subjectsOf(sel map[string][]string) []gofresh.Subject {
	var subjects []gofresh.Subject
	for pkg, names := range sel {
		for _, name := range names {
			subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: name})
		}
	}
	sortSubjects(subjects)
	return subjects
}

// sortSubjects orders subjects by package, then symbol.
func sortSubjects(subjects []gofresh.Subject) {
	sort.Slice(subjects, func(i, j int) bool {
		a, b := subjects[i], subjects[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Symbol < b.Symbol
	})
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
	// (REQ-evidence-witness-cache-format-variants).
	id   string
	tags []string
	env  []string
	// toolchain is the effective GOVERSION the invocation's one
	// environment read answered in its module root — the toolchain the
	// group's environment pins (GOTOOLCHAIN) and its engine's provenance
	// is judged by, so the engine arm samples nothing a second time.
	toolchain string
	// neverServes maps each of the group's subjects serving refuses to
	// its reason — random-seeded witnesses and unclassifiable subjects
	// (REQ-evidence-witness-freshness-seeded); resolved once per policy
	// capture, read by both witness forms' serving and publish paths.
	neverServes map[gofresh.Subject]uncacheable
	// witnessEnv is the complete compilation environment, including the derived
	// width. Runtime bindings separately own each actual package environment.
	witnessEnv []string
	// race is the group's witness tier AND a build input: the analysis
	// engine's flags must describe the binary the tests actually run as,
	// and the flag rides the build-config guard into every fingerprint,
	// so records produced at one tier can never serve the other - a
	// policy flip between race and plain_witness re-executes instead of
	// laundering the tier (REQ-evidence-witness-freshness-inputs's race flag as
	// a caller-supplied build input).
	race bool
	// moduleRoot, moduleMode, and pgo are the group's build inputs
	// beyond the selection: each partitions the group key, so one group
	// has one of each, and the engine's flags and build inputs describe
	// the binary the witnesses run as (REQ-evidence-witness-freshness-inputs's
	// caller-supplied build inputs).
	moduleRoot        string
	moduleMode        stipulatorv1.GoModuleMode
	pgo               string
	linkerAssignments []string
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
	// scratchNamespaces is the group's canonical reviewed scratch
	// namespace set, the declaration every package's ingest runs under;
	// it partitions the group key as exclusions do and re-addresses no
	// record (the admission rides the engine's proof at both ends).
	scratchNamespaces []runtimeinput.ScratchNamespace
	// invs names the group's member invocations in policy order; the
	// explain surface reports them as the answering view's identity.
	invs []string
	// packages holds each selected package's facts in one entry — the
	// one invocation of this group selecting it, its expected witness
	// set, whether a second invocation also selected it, and whether its
	// whole-package process runs solo — so the facts cannot disagree
	// about which packages the group holds.
	packages map[string]*groupPackage
	// selectedPackages retains discovery's original-package metadata for the
	// invocations sharing this engine. Default PGO belongs only to selected
	// main packages, including when the invocation omits the PGO flag.
	selectedPackages []listedPackage
	telemetrySource  string
	// view is the shared pre-execution source/build generation. Runtime
	// bindings and independent producing/checking units derive from it.
	view *witnessSnapshot
	// Each package owns its runtime binding and independent closing state.
	legs map[string]*packageLeg
}

// WitnessRecorder is the producer side of witness freshness under the
// accepted policy: it prepares source/build snapshots before execution and
// publishes independently eligible native-finalized results from each actual
// process. It is a
// cache producer only — health and witness evidence never depend on it,
// and any fault on this path degrades to publishing nothing while the
// derivation's evidence stands (the cache saves work, it never blocks or
// weakens witnessing).
type WitnessRecorder struct {
	dir      string
	degraded string
	groups   []*captureGroup
	records  []witnesscache.Record
	reasons  map[gofresh.Subject]uncacheable
}

// Close releases remaining source views and prospective runtime storage. The
// caller must first finish, or cancel and join, executions using the recorder.
func (r *WitnessRecorder) Close() {
	if r == nil {
		return
	}
	for _, g := range r.groups {
		for _, leg := range g.legs {
			leg.dispose()
		}
		g.view = nil
	}
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
// normalized forms, which discovery annotates in place (package directories,
// closure directories, and the finalized execution allocation). Configure the
// execution class and scope before the first discovery reader; every later
// reader sees the same frozen environment and selection.
type Capture struct {
	dir        string
	execution  executionScope
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
	// globalCount includes every invocation selecting a package, including
	// ineligible legs whose failures still contribute to selective execution.
	// Publication ambiguity is judged separately within each capture group.
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

// LoadCapture loads the tree's accepted policy record (or synthesizes the
// default policy when .stipulator/policy.textproto is absent) and captures it:
// the one entry every operation that consumes the policy takes. A
// record fault carries policy.ErrRecord for the caller's disposition;
// a normalization fault is the operation's.
func LoadCapture(ctx context.Context, dir string) (*Capture, error) {
	p, err := LoadPolicy(dir)
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
// (REQ-evidence-witness-freshness-seeded). One classifier call answers the
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
		g.neverServes = map[gofresh.Subject]uncacheable{}
		for _, s := range groupSubjects(g) {
			if why, ok := refusals[s.Package+"."+s.Symbol]; ok {
				// A refusal is attributed or it is not a refusal the
				// spec admits: an implementor answering with an empty
				// reason still refuses, under a reason that says so;
				// the classifier's text (the resolver wire's) is
				// admitted under the class its prefix names.
				if why == "" {
					g.neverServes[s] = reasonUnclassifiable.with("refused serving by the classifier without a stated reason")
					continue
				}
				g.neverServes[s] = parseReason(why)
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
	segments := append([]keySegment{
		{"tags", quotedList(n.Tags)},
		{"env", quotedList(sortedCopy(witnessEnvOf(n)))},
		{"modulemode", quotedValue(n.ModuleMode.String())},
		{"module_root", quotedValue(n.ModuleRoot)},
		{"pgo", quotedValue(n.PGO)},
		{"args", quotedList(identityArgs(n.Args))},
		{"pure", boolValue(n.AssumePure)},
		{"race", boolValue(n.Race)},
	}, reviewedKeySegments(n)...)
	segments = append(segments, linkerIdentitySegments(n)...)
	if len(n.LinkerAssignments) != 0 {
		segments = append(segments, keySegment{"linker_values", quotedList(n.LinkerAssignments)})
	}
	if len(n.InputFiles) != 0 {
		segments = append(segments, keySegment{"input_files", quotedList(inputFilePaths(n.InputFiles))})
	}
	return segments
}

// groupKey is one invocation's capture-group identity: the
// closure-shaping configuration (build tags, normalized environment,
// the invocation-wide purity assertion) plus the race bit — race is a
// build input and a witness-class boundary, so a race and a
// plain-witness invocation sharing tags and env must not share a
// capture group, or one group's fingerprints would describe two
// different binaries and two witness tiers.
func groupKey(n *NormalizedInvocation) string {
	return encodeSegments(groupKeySegments(n))
}

// groupIdentitySegments is the record-identity coordinate's canonical
// field table (groupIdentity encodes it): the POLICY-DECLARED build
// selection and per-invocation
// semantics — tags, the race build input, the declared platform, cgo,
// GOFLAGS and toolchain pins (empty when the invocation rides the
// ambient value), workspace and module mode, the module root (the
// go.mod that governs the build, so the same package selected from two
// roots is two coordinates), the PGO profile, the
// identity-bearing extra binary arguments (runtimeOnlyArg's reviewed
// bounds re-address nothing), and the declared environment deltas
// (order-canonicalized) — and nothing more. Every ambient-resolved fact is deliberately excluded:
// the merged ambient environment, the effective toolchain, platform,
// GOFLAGS, GOEXPERIMENT, and the delivered width are the fingerprints'
// authority — an identity digesting them would silently orphan the
// whole store on a new shell, a toolchain upgrade, or a host-width
// change (and a drifted shell's prune would delete records the normal
// shell serves), while fingerprints refuse with a named reason and
// variants coexist. Exclusions, scratch namespaces, vouches, and the purity assertion
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
	return append([]keySegment{
		{"tags", quotedList(n.Tags)},
		{"race", boolValue(n.Race)},
		{"goos", quotedValue(n.DeclaredGOOS)},
		{"goarch", quotedValue(n.DeclaredGOARCH)},
		{"cgo", quotedValue(n.DeclaredCgo)},
		{"goflags", quotedValue(n.DeclaredGOFLAGS)},
		{"workspace", boolValue(n.WorkspaceOn)},
		{"modulemode", quotedValue(n.ModuleMode.String())},
		{"module_root", quotedValue(n.ModuleRoot)},
		{"pgo", quotedValue(n.PGO)},
		{"toolchain", quotedValue(n.DeclaredToolchain)},
		{"args", quotedList(identityArgs(n.Args))},
		{"envset", quotedList(sortedCopy(n.EnvOverrides))},
		{"envdeny", quotedList(sortedCopy(n.EnvDeny))},
	}, linkerIdentitySegments(n)...)
}

// Optional declared coordinates preserve every pre-feature record address.
func linkerIdentitySegments(n *NormalizedInvocation) []keySegment {
	var segments []keySegment
	if len(n.DeclaredLinkerStrings) != 0 {
		segments = append(segments, keySegment{"linker_sources", quotedList(n.DeclaredLinkerStrings)})
	}
	if len(n.InputFiles) != 0 {
		segments = append(segments, keySegment{"input_file_environment", quotedList(inputFileNames(n.InputFiles))})
	}
	return segments
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
		out[recordstore.Digest(groupIdentity(n))] = true
	}
	return out
}

// discover is the capture's discovered leg, derived on the first call
// and held: every invocation's obligations, the policy-wide selection
// count, and the witness-eligible invocations (race-enabled, plus
// explicit plain-witness admissions) folded into capture groups, each
// carrying its tier. A fault, cancellation included, is held with it;
// ctx binds the first call only.
func (pc *Capture) discover(ctx context.Context) (*policyDiscovery, error) {
	return pc.discovered.get(func() (*policyDiscovery, error) { return discoverPolicy(ctx, pc.normalized, pc.execution) })
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

func discoverPolicy(ctx context.Context, normalized []*NormalizedInvocation, execution executionScope) (*policyDiscovery, error) {
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
			// (REQ-policy-explicit-unresolvable); this walk is the record's, so the
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
	// Every invocation's selection count is known before deciding which
	// ineligible legs can execute. Freeze all widths before grouping by env.
	for _, ic := range pc.invocations {
		freezeWitnessAllocation(ic.n, execution.packageCount(ic, pc.globalCount))
	}
	byKey := map[string]*captureGroup{}
	var keys []string
	for _, e := range entries {
		n := e.n
		tests := map[string][]string{}
		for _, o := range e.obligations {
			switch o.Kind {
			case ObligationTest, ObligationFuzz:
				tests[o.Package] = append(tests[o.Package], o.Name)
			}
		}
		key := groupKey(n)
		g := byKey[key]
		if g == nil {
			g = &captureGroup{
				toolchain:         n.Toolchain,
				telemetrySource:   n.TelemetrySource,
				id:                recordstore.Digest(groupIdentity(n)),
				tags:              n.Tags,
				env:               n.Env,
				witnessEnv:        witnessEnvOf(n),
				race:              n.Race,
				moduleRoot:        n.ModuleRoot,
				moduleMode:        n.ModuleMode,
				pgo:               n.PGO,
				linkerAssignments: slices.Clone(n.LinkerAssignments),
				assumePure:        n.AssumePure,
				vouches:           sortedCompact(n.Vouches, strings.Compare),
				excludedPaths:     canonicalReviewedExclusions(n.ExcludedPaths),
				scratchNamespaces: canonicalReviewedNamespaces(n.ScratchNamespaces),
				packages:          map[string]*groupPackage{},
			}
			byKey[key] = g
			keys = append(keys, key)
		}
		if !slices.Contains(g.invs, n.Name) {
			g.invs = append(g.invs, n.Name)
		}
		pc.invGroup[n.Name] = g
		g.selectedPackages = append(g.selectedPackages, n.selectedPackages...)
		for pkg, names := range tests {
			if prev, taken := g.packages[pkg]; taken {
				if prev.inv != n.Name {
					prev.ambiguous = true
				}
				continue
			}
			g.packages[pkg] = &groupPackage{inv: n.Name, names: names}
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
	for pkg, p := range g.packages {
		if p.ambiguous {
			continue
		}
		for _, name := range p.names {
			subjects = append(subjects, gofresh.Subject{Package: pkg, Symbol: name})
		}
	}
	sortSubjects(subjects)
	return subjects
}

// beforeGroupEngineForTest, when set, runs before a group's engine is
// constructed — the seam an ordering test uses to place the engines'
// loads against the seeding backend's release.
var beforeGroupEngineForTest func()

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
	// could only re-read it — judged through gofresh's provenance
	// composite, so a frontend that cannot read what this group's
	// toolchain builds refuses before any verdict
	// (REQ-fresh-toolchain-skew) — the go1.27 stale-binary episode's
	// structural fix, shared with pew and gomutant.
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
	policy := gofresh.SessionPolicy{
		EvidenceRoot: dir,
		Vouches:      slices.Clone(g.vouches),
		Execution:    gofresh.PackageExecution,
		Runner:       engineRunner,
		Progress: func(p gofresh.Progress) {
			emitEngineDiagnostic(p)
			progress.FromContext(ctx).Keepalive()
		},
	}
	if g.assumePure {
		policy.PureSubjects = groupSubjects(g)
	}
	// The witness command compiles as well as executes. Analyze under its
	// complete environment, including the width cap: a compiler wrapper can
	// read that value too. A split environment requires a separately built
	// artifact, which this producing path does not provide.
	flags := append(buildFlags(g.race, g.tags, g.moduleMode, buildPGO(dir, g.pgo)), linkerBuildFlags(g.linkerAssignments)...)
	return gofresh.NewSession(ctx, gofresh.BuildDescription{
		Directory:   g.directory(dir),
		Environment: g.witnessEnv,
		Flags:       flags,
		Inputs:      append([]string{}, inputs.inputs...),
	}, policy)
}

// pgoObservation is immutable after construction. The metadata query names
// only accepted import paths, under the compiling invocation's selection;
// validating it never replaces discovery's obligation set.
type pgoObservation struct {
	root     string
	pgo      string
	inputs   []string
	selected []listedPackage
	query    *NormalizedInvocation
}

// buildInputDrift records a comparison that demonstrated movement of an
// already observed compilation input. Read/analysis failures remain distinct.
type buildInputDrift struct {
	detail string
	cause  error
}

func (e *buildInputDrift) Error() string { return e.detail }
func (e *buildInputDrift) Unwrap() error { return e.cause }

func observePGO(ctx context.Context, root string, g *captureGroup) (*pgoObservation, error) {
	o := &pgoObservation{root: root, pgo: g.pgo, selected: pgoPackageFacts(g.selectedPackages)}
	if (g.pgo == "" || g.pgo == "auto") && len(o.selected) != 0 {
		var paths []string
		for _, p := range o.selected {
			paths = append(paths, p.ImportPath)
		}
		o.query = &NormalizedInvocation{
			Name: strings.Join(g.invs, ","), Dir: g.directory(root),
			Env: slices.Clone(g.witnessEnv), Tags: slices.Clone(g.tags), Race: g.race,
			ModuleMode: g.moduleMode, Packages: slices.Compact(paths), TelemetrySource: g.telemetrySource,
		}
		if err := o.validateSelection(ctx); err != nil {
			return nil, err
		}
	}
	var err error
	o.inputs, err = pgoBuildInputs(ctx, root, g.pgo, o.selected)
	return o, err
}

// pgoPackageFacts drops enumeration details that cannot select a profile and
// canonicalizes the remaining original-package facts for equality judgment.
func pgoPackageFacts(packages []listedPackage) []listedPackage {
	result := make([]listedPackage, 0, len(packages))
	for _, p := range packages {
		result = append(result, listedPackage{ImportPath: p.ImportPath, Name: p.Name, Dir: p.Dir})
	}
	slices.SortFunc(result, func(a, b listedPackage) int {
		if c := strings.Compare(a.ImportPath, b.ImportPath); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Dir, b.Dir)
	})
	return slices.CompactFunc(result, samePGOPackage)
}

func samePGOPackage(a, b listedPackage) bool {
	return a.ImportPath == b.ImportPath && a.Name == b.Name && a.Dir == b.Dir
}

func (o *pgoObservation) validateSelection(ctx context.Context) error {
	if o.query == nil {
		return ctx.Err()
	}
	current, err := listPackageMetadata(ctx, o.query)
	if err != nil {
		return fmt.Errorf("pgo selected package metadata: %w", err)
	}
	for _, pkg := range current {
		if pkg.Error != nil {
			return fmt.Errorf("pgo selected package metadata for %s: %s", pkg.ImportPath, pkg.Error.Err)
		}
		if pkg.Name == "" || pkg.Dir == "" {
			return fmt.Errorf("pgo selected package metadata incomplete for %s", pkg.ImportPath)
		}
	}
	if !slices.EqualFunc(o.selected, pgoPackageFacts(current), samePGOPackage) {
		return &buildInputDrift{detail: fmt.Sprintf("pgo selected package metadata changed for %s", strings.Join(o.query.Packages, ", "))}
	}
	return nil
}

func (o *pgoObservation) validate(ctx context.Context) error {
	if o == nil {
		return ctx.Err()
	}
	if err := o.validateSelection(ctx); err != nil {
		return err
	}
	current, err := pgoBuildInputs(ctx, o.root, o.pgo, o.selected)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &buildInputDrift{detail: err.Error(), cause: err}
		}
		return err
	}
	if !slices.Equal(current, o.inputs) {
		return &buildInputDrift{detail: fmt.Sprintf("pgo build inputs changed: opening %v; current %v", o.inputs, current)}
	}
	return nil
}

func (g *captureGroup) directory(root string) string {
	return filepath.Join(root, filepath.FromSlash(g.moduleRoot))
}

// buildPGO resolves a declared profile from the corpus root for every
// analysis or producing command, independently of its module directory.
func buildPGO(root, value string) string {
	if value == "" || value == "auto" || value == "off" {
		return value
	}
	return filepath.Join(root, filepath.FromSlash(value))
}

// newEngine is the one gofresh session constructor for source resolution: the
// analysis directory, the view's environment and build flags, the evidence
// root, and the operation's progress seam — freshness capture and validation
// are the longest silent stretches of a run, so gofresh's own analysis steps
// feed the seam as rate-limited keepalives and the engine diagnostic face.
func newEngine(ctx context.Context, dir string, env, flags []string, evidenceRoot string) (*gofresh.Session, error) {
	return gofresh.NewSession(ctx, gofresh.BuildDescription{
		Directory:   dir,
		Environment: env,
		Flags:       flags,
		Inputs:      []string{},
	}, gofresh.SessionPolicy{
		EvidenceRoot: evidenceRoot,
		// The engine's own go commands ride the owned boundary
		// (REQ-go-owned-processes-runner); the loader's go list children spawn
		// through x/tools outside any runner.
		Runner: engineRunner,
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
// reach — gofresh's own rendering (Progress.Diagnostic: a multi-line
// detail folded onto one line), serialized per sink, built once
// beside the writer and never per engine; it writes through the
// swappable engineDiagnostics at call time, so a test's swap is
// honoured.
var engineDiagnosticSink = gofresh.DiagnosticsTo(writerFunc(func(p []byte) (int, error) { return engineDiagnostics.Write(p) }))

// emitEngineDiagnostic writes a payload-bearing gofresh event
// (per-subject analysis-unavailable provenance, the unlisted-toolchain
// notice) to the operator's log unthrottled; detail-free keep-alives
// stay silent — the keep-alive seam carries no message, and discarding
// diagnostics by signature once spent the engine's one toolchain
// announcement into a void.
func emitEngineDiagnostic(p gofresh.Progress) { engineDiagnosticSink(p) }

// NewWitnessRecorder prepares freshness publication for one execution of
// the accepted policy: it must be called before the policy executes, so
// the shared snapshots pin the tree the execution compiles, and
// the caller must close the recorder after executions have joined and report
// every package's completion under its covering
// invocation through the completion hook — a package whose completion
// is never reported publishes nothing, with no fallback. Only race-enabled
// or explicitly plain-admitted Go invocations are captured. An unadmitted
// non-race invocation grants no witness evidence, so nothing it produces may
// enter the cache a freshness-serving run would grant evidence from. The seeding
// classification is the recorder's one question to its backend, and the
// backend is released right after it (verify.Quiescer) — before the
// engines' loads below, so the backend's child is never resident beside
// them or the execution. A fault while preparing disables publication
// and is reported through the derived run's degraded reason, never as
// an error — publication is optimization, not correctness — with one
// exception: a toolchain-provenance refusal returns as the error, a
// run-level abort (REQ-evidence-toolchain-provenance; classifyFault),
// because the refused frontend also discovered and selected the suite
// the degraded run would execute.
func NewWitnessRecorder(ctx context.Context, pc *Capture, seeding verify.WitnessSeeding) (*WitnessRecorder, error) {
	if err := pc.ConfigureExecution(true, nil); err != nil {
		return nil, err
	}
	dir := pc.dir
	r := &WitnessRecorder{dir: dir, reasons: map[gofresh.Subject]uncacheable{}}
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
	defer func() {
		if r.degraded != "" {
			for _, g := range d.groups {
				for _, leg := range g.legs {
					leg.dispose()
				}
				g.view = nil
			}
		}
	}()
	// A seeding-classification fault degrades the run exactly as an
	// engine fault does: nothing publishes, so nothing can later serve
	// a witness the fault left unclassified (fail closed).
	seedingErr := classifySeeded(d, seeding)
	// The classification was this form's last question to its backend:
	// released before the engines' loads and the first spawn
	// (REQ-evidence-resolution-freshness-quiesce).
	quiesceSeeding(seeding)
	if seedingErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degraded = seedingErr.Error()
		return r, nil
	}
	// A package two invocations of one group select has no producing
	// leg: the refusal is a discovery fact, recorded once here for
	// every group — the group a double selection empties never
	// publishes, so its subjects' reason has no later home. The first
	// selecting invocation's names are every subject: the group key
	// holds every dimension that moves a package's listed tests, so two
	// invocations of one group list one package identically.
	for _, g := range d.groups {
		for pkg, p := range g.packages {
			if !p.ambiguous {
				continue
			}
			for _, name := range p.names {
				r.reasons[gofresh.Subject{Package: pkg, Symbol: name}] = reasonNoProducingLeg
			}
		}
	}
	for g, subjects := range d.populatedGroups() {
		view, inputs, err := groupSnapshot(ctx, dir, g)
		if err != nil {
			return degrade(err)
		}
		g.view = view
		byPkg := map[string][]gofresh.Subject{}
		for _, s := range subjects {
			byPkg[s.Package] = append(byPkg[s.Package], s)
		}
		g.legs = map[string]*packageLeg{}
		for pkg := range byPkg {
			leg, err := nativePackageLeg(ctx, g.view, inputs, pc.byName()[g.selectingInvocation(pkg)], pkg)
			if err != nil {
				return degrade(err)
			}
			g.legs[pkg] = leg
		}
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
func (r *WitnessRecorder) Derive(ctx context.Context, report *stipulatorv1.ExecutionReport) (*verify.TestRun, error) {
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
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
		}
		for key := range executedTop {
			if installed[key] {
				delete(tr.UncacheableReasons, key)
				continue
			}
			tr.Uncached++
			if _, ok := tr.UncacheableReasons[key]; !ok {
				tr.UncacheableReasons[key] = reasonDegraded.with(degraded).String()
			}
		}
	case len(r.groups) == 0:
		// Nothing was capturable (no witness-eligible invocation, no
		// expected tests, or every eligible package doubly selected):
		// every executed test is uncacheable — under its recorded
		// refusal where one stands — and the existing cache is left
		// alone.
		tr.Uncached = tr.Ran
		tr.UncacheableReasons = map[string]string{}
		for s, why := range uncacheableWhy {
			tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
		}
		for key := range executedTop {
			if _, ok := tr.UncacheableReasons[key]; !ok {
				tr.UncacheableReasons[key] = reasonNoCapture.with("no witness-eligible invocation covers the package").String()
			}
		}
	default:
		// Published is a record count (one per group holding the
		// subject); Uncached is a subject count in Ran's unit, so
		// distinct published subjects are the subtrahend — two group
		// records for one shared package must not mask another
		// subject's drop.
		publishedSubjects := map[string]bool{}
		for _, rec := range published {
			publishedSubjects[rec.Package+"."+rec.Test] = true
		}
		if tr.Ran > len(publishedSubjects) {
			tr.Uncached = tr.Ran - len(publishedSubjects)
		}
		// Per-test attribution mirrors the selective runner's: the
		// ladder's own refusal reasons plus a structural fallback
		// (REQ-evidence-witness-freshness-diagnosable's attributable
		// reason).
		if tr.Uncached > 0 || len(uncacheableWhy) > 0 {
			tr.UncacheableReasons = map[string]string{}
			for s, why := range uncacheableWhy {
				tr.UncacheableReasons[s.Package+"."+s.Symbol] = why.String()
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
					tr.UncacheableReasons[key] = reasonNotPublished.with("").String()
				}
			}
		}
		// Publication installed at production: each package's records
		// landed as their own variant files the moment the package
		// completed under its covering invocation. Records this
		// run never touched — a shadowed sibling's, a package this policy
		// never selected's — need no rewrite: the store is per-record, so
		// retention is the default and nothing shrinks. A departed test's
		// variants linger as dead weight — its identity never installs
		// again, so no bound fires; store growth is cost, never
		// correctness.
	}
	return tr, nil
}

// packageCompleted is the completion hook of the health-judged form:
// the moment a package's process has completed under its covering
// invocation, every group holding the package under that invocation
// publishes the package's records — from the process's own rows,
// disposition and observation — and installs them at once, named on
// the progress stream by invocation and package: the unit of
// persistence is the package under its covering invocation on this
// form as on the selective one (REQ-policy-cancellation-unit). A
// publication fault degrades the run's further publication, as a
// group's did; the error return is reserved for caller cancellation.
func (r *WitnessRecorder) packageCompleted(ctx context.Context, invocation string, unit packageUnit) error {
	if r.degraded != "" {
		return nil
	}
	// The package's one producing process under its covering invocation
	// is every subject's single candidate, rows or none: the executor
	// launches exactly one process per selected package per invocation,
	// and a package with no row still carries its disposition.
	candidate := producerCandidate{
		healthy: unit.run.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		rows:    unit.run.tests,
		obs:     unit.run.obs,
	}
	var soloCandidates []producerCandidate
	for _, s := range unit.solos {
		soloCandidates = append(soloCandidates, producerCandidate{
			healthy: s.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
			rows:    s.tests,
			obs:     s.obs,
		})
	}
	for _, g := range r.groups {
		// One package can sit in several groups — two invocations under
		// different build coordinates both selecting it — each group
		// running it under its own invocation; the completing invocation
		// publishes the package in its own group alone. A package two
		// invocations of one group select was refused at discovery.
		p, ok := g.packages[unit.pkg]
		if !ok || p.ambiguous || p.inv != invocation {
			continue
		}
		// Every non-ambiguous package of a populated group has a leg —
		// its entry carries at least one expected test, and the legs
		// were built over the group's subjects — and completes once
		// under its invocation, so the leg is not yet published.
		records, reasons, degraded, err := r.publishPackage(ctx, g, unit.pkg, g.legs[unit.pkg], candidate, soloCandidates)
		if err != nil {
			return err
		}
		if degraded != "" {
			r.degrade(degraded)
			return nil
		}
		maps.Copy(r.reasons, reasons)
		landed, installErr := installRecords(ctx, r.dir, records, r.reasons)
		r.records = append(r.records, landed...)
		// What landed is named before anything else: a cancellation
		// between two packages' installs must not leave records on disk
		// the ending never mentions.
		if len(landed) > 0 {
			progress.FromContext(ctx).Persisted(invocation+" "+unit.pkg, len(landed))
		}
		if installErr != nil {
			return installErr
		}
	}
	return nil
}

// publish is the run's publication account: every record the
// completion hook installed and every refusal reason, with the degraded
// reason when a later package's fault ended publication. Nothing is
// left to publish here: the recorder's groups are the populated groups
// of the discovery the executor walks, each holding a package exactly
// one of its invocations selects, and the executor fires every
// package's completion under its invocation or returns its fault
// before derivation — so every package is published by the time the
// last invocation completes.
func (r *WitnessRecorder) publish() ([]witnesscache.Record, map[gofresh.Subject]uncacheable, string) {
	return append([]witnesscache.Record(nil), r.records...), maps.Clone(r.reasons), r.degraded
}

// publishPackage selects native-finalized records from this package's actual
// process and applies the consumer's outcome and opaque-input gates.
// The leg is released after — nothing serves on this form, so
// nothing reads it again — and the group's view with it once every
// leg has published. A source-view, configuration or infrastructure fault ends
// further publication with its named cause; earlier packages keep what their
// own legs independently validated. Demonstrated runtime-input movement refuses
// this process's records locally; its account and execution facts remain intact.
// The error return is reserved for caller cancellation.
func (r *WitnessRecorder) publishPackage(ctx context.Context, g *captureGroup, pkg string, leg *packageLeg, candidate producerCandidate, soloCandidates []producerCandidate) ([]witnesscache.Record, map[gofresh.Subject]uncacheable, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, "", err
	}
	defer g.release(pkg)
	if candidate.obs != nil && candidate.obs.closeErr != nil && !runtimeInputMovement(candidate.obs.closeErr) {
		if ctx.Err() != nil {
			return nil, nil, "", ctx.Err()
		}
		return nil, nil, "runtime producer validation failed: " + candidate.obs.closeErr.Error(), nil
	}
	for _, sc := range soloCandidates {
		if sc.obs != nil && sc.obs.closeErr != nil && !runtimeInputMovement(sc.obs.closeErr) {
			if ctx.Err() != nil {
				return nil, nil, "", ctx.Err()
			}
			return nil, nil, "runtime producer validation failed: " + sc.obs.closeErr.Error(), nil
		}
	}
	reasons := map[gofresh.Subject]uncacheable{}
	eligible := map[gofresh.Subject]*pubSubject{}
	var order []gofresh.Subject
	candidates := []producerCandidate{candidate}
	for _, name := range g.packages[pkg].names {
		subject := gofresh.Subject{Package: pkg, Symbol: name}
		why, refused := g.neverServes[subject]
		subCandidates := candidates
		if leg != nil && leg.view != nil && leg.view.isSolo(subject) && len(soloCandidates) > 0 {
			subCandidates = soloCandidates
		}
		ps, reason := judgeSubject(subject, why, refused, true, subCandidates)
		if ps == nil {
			reasons[subject] = reason
			continue
		}
		eligible[subject] = ps
		order = append(order, subject)
	}
	sortSubjects(order)
	// Native closing already supplied the evidence. The shared publication
	// path applies the remaining consumer input and record-assembly gates.
	records, closeFault, fatal := publishEligible(ctx, g.id, leg, order, eligible, declarationsFor(g), reasons)
	if fatal != nil {
		return nil, nil, "", fatal
	}
	if closeFault != nil {
		return nil, nil, reasonSourceFailed.with(closeFault.Error()).String(), nil
	}
	return records, reasons, "", nil
}

// degrade ends the run's further publication with the named cause and
// releases every leg still held: nothing publishes after a degrade, so
// no analysis view is read again and none is carried to the run's end.
func (r *WitnessRecorder) degrade(reason string) {
	r.degraded = reason
	for _, g := range r.groups {
		for pkg := range g.legs {
			g.release(pkg)
		}
	}
}

// release drops one package's leg and, once every leg of the group has
// released, the group's view: the memory a published package's analysis
// held returns while its siblings still execute.
func (g *captureGroup) release(pkg string) {
	if leg := g.legs[pkg]; leg != nil {
		leg.release()
	}
	if legsReleased(g.legs) {
		g.view = nil
	}
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
	if rep == nil {
		rep = progress.New(nil)
		ctx = progress.NewContext(ctx, rep)
	}
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
	defer recorder.Close()
	proofs := processProofs{}
	for _, g := range recorder.groups {
		proofs.add(g, g.legs)
	}
	report, _, err := executePolicy(ctx, pc, func(invocation string, unit packageUnit) error {
		return recorder.packageCompleted(ctx, invocation, unit)
	}, proofs)
	if err != nil {
		return nil, nil, err
	}
	// Producer validation and publication judge the evidence the run
	// produced: verification-phase work.
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	tr, err := recorder.Derive(ctx, report)
	if err != nil {
		return nil, nil, err
	}
	// The witness-eligible selection boundary holds on this form
	// exactly as on the selective one — one classification on the
	// discovery, the run's outcomes as the evidence and the report's
	// invocation health as the cause (REQ-check-witness-selection). The
	// universe is the one the execution itself selected against — the
	// capture's, discovered before the suite ran: a test the run could
	// only have created mid-execution predates no policy, and a source
	// edit mid-run discards the run's records anyway. A universe fault
	// degrades silently: without it only the invocations' own
	// obligations classify.
	d, err := pc.discover(ctx) // the held leg: discovered before the suite ran
	if err != nil {
		return nil, nil, err
	}
	universe, _ := pc.ObligationUniverse(ctx)
	health := reportPackageHealth(report)
	classes := d.classifyWitnesses(universe, executedTopPackages(report), tr.Outcomes, func(invocation, pkg string) (string, bool) {
		disposition, ok := health[invPkgKey(invocation, pkg)]
		if !ok {
			return "", false
		}
		return dispositionCause(invocation, pkg, disposition, packageReason(report.GetDiagnostics(), invocation, pkg, disposition)), true
	})
	tr.OutsideSubjects = classes.outside
	tr.OutsidePolicy = len(classes.outside)
	tr.NoOutcome = classes.noOutcome
	published, _, _ := recorder.publish()
	tr.InvocationWitnesses = buildInvocationWitnessAccounts(
		d,
		report.GetTests(),
		nil,
		publishedByInvocation(d.groups, published),
		tr.NoOutcome,
		nil,
	)
	tr.Timing = rep.TimingDiagnostics()
	report.SetTiming(tr.Timing)
	return report, tr, nil
}

func publishedByInvocation(groups []*captureGroup, published []witnesscache.Record) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, rec := range published {
		key := rec.Package + "." + rec.Test
		for _, g := range groups {
			if g.id != rec.Group {
				continue
			}
			if inv := g.selectingInvocation(rec.Package); inv != "" {
				if out[inv] == nil {
					out[inv] = map[string]bool{}
				}
				out[inv][key] = true
			}
		}
	}
	return out
}

func buildInvocationWitnessAccounts(
	d *policyDiscovery,
	rows []*stipulatorv1.TestResult,
	servedByInv map[string]map[string]bool,
	publishedByInv map[string]map[string]bool,
	noOutcome map[string]string,
	scopeSkipped map[string]bool,
) []*stipulatorv1.InvocationWitnessAccount {
	if d == nil {
		return nil
	}
	executedByInv := map[string]map[string]bool{}
	for _, row := range rows {
		inv := row.GetProducer().GetInvocation()
		if inv == "" {
			continue
		}
		key, ok := executedTopKey(row.GetPackage(), row.GetTest())
		if !ok {
			continue
		}
		if executedByInv[inv] == nil {
			executedByInv[inv] = map[string]bool{}
		}
		executedByInv[inv][key] = true
	}
	out := make([]*stipulatorv1.InvocationWitnessAccount, 0, len(d.invocations))
	for _, ic := range d.invocations {
		inv := ic.n.Name
		served := servedByInv[inv]
		execSet := executedByInv[inv]
		pubSet := publishedByInv[inv]
		uncacheable := 0
		for key := range execSet {
			if !pubSet[key] && !served[key] {
				uncacheable++
			}
		}
		noOutCount := 0
		if ic.n.WitnessEligible() {
			seen := map[string]bool{}
			for _, o := range ic.obligations {
				if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
					continue
				}
				key := o.Package + "." + o.Name
				if seen[key] || scopeSkipped[key] {
					continue
				}
				seen[key] = true
				if _, missing := noOutcome[key]; missing {
					noOutCount++
				}
			}
		}
		acc := &stipulatorv1.InvocationWitnessAccount{}
		acc.SetInvocation(inv)
		acc.SetWitnessEligible(ic.n.WitnessEligible())
		acc.SetPlainWitness(ic.n.PlainWitness)
		acc.SetServed(int32(len(served)))
		acc.SetExecuted(int32(len(execSet)))
		acc.SetUncacheable(int32(uncacheable))
		acc.SetNoOutcome(int32(noOutCount))
		out = append(out, acc)
	}
	return out
}

// reportPackageHealth indexes the report's package dispositions by
// invocation and package.
func reportPackageHealth(report *stipulatorv1.ExecutionReport) map[string]stipulatorv1.HealthDisposition {
	health := map[string]stipulatorv1.HealthDisposition{}
	for _, h := range report.GetInvocations() {
		for _, p := range h.GetPackages() {
			health[invPkgKey(h.GetInvocation(), p.GetPackage())] = p.GetDisposition()
		}
	}
	return health
}

// witnessClasses is one classification of every expected witness
// subject: outside when no eligible invocation selects it; no witness
// outcome, with the execution-layer cause, when the eligible selection
// covers it but the run granted it nothing (REQ-check-witness-selection).
type witnessClasses struct {
	outside   map[string]bool
	noOutcome map[string]string
}

// classifyWitnesses is the one rule both evidence forms answer with. The
// expected subjects are the universe's test and fuzz obligations and
// every invocation's own; an executed subject the universe lacks is
// expected too (executed maps its key to its package). A subject is
// outside when no witness-eligible invocation selects it. An
// eligible-covered subject with no outcome — no result, or a result
// its package's process denied a grant — names its cause: each
// eligible invocation selecting it with the package's disposition
// under that invocation, from causeFor; an invocation with no
// disposition reported it no result. A nil causeFor classes the
// outside set alone — the selective form's pre-run count.
func (d *policyDiscovery) classifyWitnesses(universe []Obligation, executed map[string]string, outcomes map[string]verify.TestOutcome, causeFor func(invocation, pkg string) (string, bool)) witnessClasses {
	expected := map[string]string{} // key -> package
	for _, o := range universe {
		if o.Kind == ObligationTest || o.Kind == ObligationFuzz {
			expected[o.Package+"."+o.Name] = o.Package
		}
	}
	eligibleBy := map[string][]string{} // key -> the eligible invocations selecting it
	for _, ic := range d.invocations {
		for _, o := range ic.obligations {
			if o.Kind != ObligationTest && o.Kind != ObligationFuzz {
				continue
			}
			key := o.Package + "." + o.Name
			expected[key] = o.Package
			if ic.n.WitnessEligible() {
				eligibleBy[key] = append(eligibleBy[key], ic.n.Name)
			}
		}
	}
	for key, pkg := range executed {
		if _, ok := expected[key]; !ok {
			expected[key] = pkg
		}
	}
	classes := witnessClasses{outside: map[string]bool{}, noOutcome: map[string]string{}}
	for key, pkg := range expected {
		invocations := eligibleBy[key]
		if len(invocations) == 0 {
			classes.outside[key] = true
			continue
		}
		if causeFor == nil {
			continue
		}
		if _, ok := outcomes[key]; ok {
			continue
		}
		var causes []string
		for _, inv := range invocations {
			if cause, ok := causeFor(inv, pkg); ok {
				causes = append(causes, cause)
			} else {
				causes = append(causes, fmt.Sprintf("invocation %s reported no result for package %s", inv, pkg))
			}
		}
		sort.Strings(causes)
		classes.noOutcome[key] = strings.Join(causes, "; ")
	}
	return classes
}

// executedTopKeys is the executed top-level witness-subject key set —
// "pkg.TopLevelTest" per report row, examples excluded by the
// toolchain's own dispatch rule, the same rule Ran counts by: the
// attribution map and the Ran count must never desynchronize.
func executedTopKeys(report *stipulatorv1.ExecutionReport) map[string]bool {
	keys := map[string]bool{}
	for key := range executedTopPackages(report) {
		keys[key] = true
	}
	return keys
}

// executedTopPackages is executedTopKeys with each key's package, as
// the row states it.
func executedTopPackages(report *stipulatorv1.ExecutionReport) map[string]string {
	keys := map[string]string{}
	for _, row := range report.GetTests() {
		if key, ok := executedTopKey(row.GetPackage(), row.GetTest()); ok {
			keys[key] = row.GetPackage()
		}
	}
	return keys
}

// pgoBuildInputs observes the profiles cmd/go selects for the original
// packages, before it constructs test binaries. Omission uses Go's default
// auto mode: only an original main package consumes its own default.pgo.
// Missing defaults are explicit inputs so their appearance moves the guard;
// a named profile must exist. Other stat/read failures refuse even though
// cmd/go skips a default on any stat error: uncertainty is not absence proof.
// These are opaque input values, not engine-watched files; a caller retaining
// an engine across execution must re-observe them before accepting its results.
func pgoBuildInputs(ctx context.Context, dir, pgo string, selected []listedPackage) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pgo == "off" {
		return nil, nil
	}
	optional := pgo == "" || pgo == "auto"
	var paths []string
	if optional {
		for _, pkg := range selected {
			if pkg.Name == "" || pkg.Dir == "" {
				return nil, fmt.Errorf("pgo auto: incomplete selected package metadata for %q", pkg.ImportPath)
			}
			if pkg.Name == "main" {
				paths = append(paths, filepath.Join(pkg.Dir, "default.pgo"))
			}
		}
	} else {
		paths = []string{buildPGO(dir, pgo)}
	}
	sort.Strings(paths)
	paths = slices.Compact(paths)
	var inputs []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil, fmt.Errorf("pgo profile %s: %w", path, err)
		}
		identity := fmt.Sprintf("pgo:%q=", filepath.ToSlash(rel))
		info, err := os.Stat(path)
		if optional && errors.Is(err, fs.ErrNotExist) {
			inputs = append(inputs, identity+"absent")
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("pgo profile %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("pgo profile %s: not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("pgo profile %s: %w", path, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inputs = append(inputs, fmt.Sprintf("%s%x", identity, sha256.Sum256(data)))
	}
	return inputs, nil
}

// dispositionCause spells one package's disposition under its
// invocation as an execution-layer cause: "invocation race: package p
// timeout", or, healthy, that the invocation's process granted the
// subject nothing.
func dispositionCause(invocation, pkg string, disposition stipulatorv1.HealthDisposition, reason string) string {
	if disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		return fmt.Sprintf("invocation %s: package %s healthy, no result for it", invocation, pkg)
	}
	text := strings.ToLower(strings.TrimPrefix(disposition.String(), "HEALTH_DISPOSITION_"))
	cause := fmt.Sprintf("invocation %s: package %s %s", invocation, pkg, strings.ReplaceAll(text, "_", " "))
	if reason != "" {
		cause += ": " + reason
	}
	return cause
}
