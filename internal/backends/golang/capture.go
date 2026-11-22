package golang

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

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
	scratchNamespaces []gofresh.ScratchNamespace
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
	// sessionState shares the group's gofresh.Session across retry-wave
	// subset copies when pgo inputs have not changed, preserving Session.localFacts.
	sessionState *groupSessionState
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
		g.legs = nil
		g.view = nil
		g.sessionState = nil
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
