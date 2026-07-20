// Package coverage evaluates verified facts into per-requirement buckets
// and runs the gate.
//
// Coverage is a policy function: (clause kind, normative keyword) decides
// the minimum evidence, the verifier's per-binding facts decide what
// evidence exists, and every non-exempt requirement lands in exactly one
// bucket. The gate is a single rule: it fails exactly when some
// requirement is red and no gap record names it. There are deliberately no
// aggregate percentages anywhere.
package coverage

import (
	"fmt"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Bucket classifies a requirement's coverage.
type Bucket int

const (
	Uncovered Bucket = iota
	Stale
	Broken
	Covered
	// Exempt: a MAY requirement with no bindings.
	Exempt
	// Attested: satisfied only by an attestation on a cell that admits
	// one — the weakest evidence, rendered distinctly, never folded into
	// covered (REQ-evidence-attestation).
	Attested
)

func (b Bucket) String() string {
	switch b {
	case Covered:
		return "covered"
	case Broken:
		return "broken"
	case Stale:
		return "stale"
	case Exempt:
		return "exempt"
	case Attested:
		return "attested"
	}
	return "uncovered"
}

// GapState classifies a gap record.
type GapState int

const (
	// Open: the landing condition does not hold yet.
	Open GapState = iota
	// Due: the landing condition holds — the deferred work is ready.
	Due
	// Resolved: the gap's requirement is covered — and, for a manual
	// landing condition, the condition has been explicitly fired; the
	// record awaits pruning.
	Resolved
)

func (s GapState) String() string {
	switch s {
	case Due:
		return "due"
	case Resolved:
		return "resolved"
	}
	return "open"
}

// Requirement is one requirement's evaluated coverage.
type Requirement struct {
	Id      string
	Kind    stipulatorv1.ClauseKind
	Keyword stipulatorv1.Keyword
	Bucket  Bucket
	// Reasons explain red buckets, deterministic order.
	Reasons []string
}

// Gap is one gap record's evaluated state.
type Gap struct {
	Path          string
	RequirementId string
	State         GapState
}

// Report is the coverage evaluation and gate verdict.
type Report struct {
	Requirements []Requirement
	Gaps         []Gap
	// PolicyOverrides lists the manifest's active overrides,
	// human-readable and canonically ordered: contract-tier
	// configuration is surfaced in every coverage output.
	PolicyOverrides []string
	// Violations lists red requirements no gap names: the gate fails
	// exactly when this is non-empty.
	Violations []string
}

// GatePasses reports the gate verdict.
func (r *Report) GatePasses() bool { return len(r.Violations) == 0 }

// GapCounts tallies gaps by disposition among the kept requirements
// (keep == nil counts all): open is the unresolved count, resolved is the
// prunable count. The summary's counters and the gate's prunable hint both
// derive from this one tally, so the two surfaces cannot drift.
func GapCounts(gaps []Gap, keep map[string]bool) (open, resolved int) {
	for _, g := range gaps {
		if keep != nil && !keep[g.RequirementId] {
			continue
		}
		if g.State == Resolved {
			resolved++
		} else {
			open++
		}
	}
	return
}

// Policy resolves each (clause kind, keyword) cell to its minimum
// evidence: manifest overrides win over the default table
// (REQ-coverage-policy). A nil Policy is the pure default table.
type Policy struct {
	overrides map[policyCell]stipulatorv1.MinimumEvidence
}

type policyCell struct {
	kind stipulatorv1.ClauseKind
	kw   stipulatorv1.Keyword
}

// PolicyFromManifest builds the effective policy from the manifest's
// override entries. Entries with an unspecified minimum are ignored
// rather than silently exempting a cell; a duplicate cell is refused —
// a self-contradictory policy must never resolve by entry order.
func PolicyFromManifest(m *stipulatorv1.Manifest) (*Policy, error) {
	p := &Policy{overrides: map[policyCell]stipulatorv1.MinimumEvidence{}}
	for _, o := range m.GetPolicy() {
		if o.GetMinimum() == stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_UNSPECIFIED {
			continue
		}
		cell := policyCell{o.GetKind(), o.GetKeyword()}
		if _, dup := p.overrides[cell]; dup {
			return nil, fmt.Errorf("manifest policy names the cell (%s, %s) twice", kindName(cell.kind), keywordName(cell.kw))
		}
		p.overrides[cell] = o.GetMinimum()
	}
	return p, nil
}

// Active renders the policy's overrides human-readably, canonically
// ordered: contract-tier configuration is surfaced, never silent.
func (p *Policy) Active() []string {
	if p == nil {
		return nil
	}
	var out []string
	for cell, min := range p.overrides {
		out = append(out, fmt.Sprintf("policy override: (%s, %s) -> %s",
			kindName(cell.kind), keywordName(cell.kw),
			strings.ToLower(strings.TrimPrefix(min.String(), "MINIMUM_EVIDENCE_"))))
	}
	sort.Strings(out)
	return out
}

func kindName(k stipulatorv1.ClauseKind) string {
	return strings.ToLower(strings.TrimPrefix(k.String(), "CLAUSE_KIND_"))
}

func keywordName(k stipulatorv1.Keyword) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(k.String(), "KEYWORD_"), "_", " "))
}

// minimum reports the overridden minimum for a cell, if any.
func (p *Policy) minimum(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) (stipulatorv1.MinimumEvidence, bool) {
	if p == nil {
		return stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_UNSPECIFIED, false
	}
	m, ok := p.overrides[policyCell{kind, kw}]
	return m, ok
}

// evidence is what the verifier's facts grant one requirement; the
// policy's proof legs are contract (REQ-coverage-policy-default), not
// dead code.
type evidence struct {
	example, property, static, proof bool
	// attested: a current, reason-carrying attestation exists — the
	// weakest rung, granted only where a policy cell admits it and never
	// aggregated into the stronger kinds above.
	attested      bool
	attestReasons []string
	broken, stale bool
	reasons       []string
}

// witness reports any executed witness, of either class.
func (e *evidence) witness() bool { return e.example || e.property }

// Evaluate computes buckets, gap states, and the gate verdict. witnessed
// states whether the verify run executed tests: without witnesses, no
// witness-tier evidence exists. pol carries the manifest's policy
// overrides; nil means the default table.
func Evaluate(spec *stipulatorv1.Spec, vr *verify.Report, store *records.Store, witnessed bool, pol *Policy) *Report {
	ev := map[string]*evidence{}
	get := func(id string) *evidence {
		e, ok := ev[id]
		if !ok {
			e = &evidence{}
			ev[id] = e
		}
		return e
	}

	for _, r := range vr.Results {
		e := get(r.RequirementId)
		if !r.ContentPinned {
			e.stale = true
			e.reasons = append(e.reasons, fmt.Sprintf("binding %s has a stale content pin", r.Symbol))
		}
		switch r.Resolution {
		case verify.NotFound:
			e.broken = true
			e.reasons = append(e.reasons, fmt.Sprintf("symbol %s not found", r.Symbol))
		case verify.Resolved:
			switch r.Shape {
			case verify.ShapeMismatch:
				e.broken = true
				e.reasons = append(e.reasons, fmt.Sprintf("shape of %s moved", r.Symbol))
			case verify.ShapeUnpinned:
				e.stale = true
				e.reasons = append(e.reasons, fmt.Sprintf("binding %s has no shape pin", r.Symbol))
			case verify.ShapeMatch:
				if r.ContentPinned {
					e.static = true
				}
			}
		}
		if (r.Role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS ||
			r.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES) && witnessed {
			switch r.TestOutcome {
			case verify.TestPassed:
				if r.ContentPinned && r.Resolution == verify.Resolved {
					switch {
					case r.WitnessClass == verify.AnalyzerProof:
						e.proof = true
					case r.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES:
						// A proves claim whose symbol no longer resolves
						// as an analyzer proof grants nothing — never
						// example evidence — and the report names the
						// drift, not just the missing evidence.
						e.reasons = append(e.reasons, fmt.Sprintf("proves claim %s passed but no longer classifies as an analyzer proof", r.Symbol))
					case r.WitnessClass == verify.PropertyWitness:
						e.property = true
					default:
						e.example = true
					}
				}
			case verify.TestFailed:
				e.broken = true
				e.reasons = append(e.reasons, fmt.Sprintf("bound test %s failed", r.Symbol))
			case verify.TestNotRun:
				e.broken = true
				e.reasons = append(e.reasons, fmt.Sprintf("bound test %s produced no outcome (unwitnessed)", r.Symbol))
			}
		}
	}

	// Attestations enter as the weakest evidence leg: a stale pin is
	// claim hygiene like any other record, a current one carries its
	// reason forward — never into the stronger kinds.
	for _, a := range vr.Attestations {
		e := get(a.RequirementId)
		if !a.ContentPinned {
			e.stale = true
			e.reasons = append(e.reasons, "attestation has a stale content pin (the requirement moved since it was vouched for)")
			continue
		}
		e.attested = true
		e.attestReasons = append(e.attestReasons, a.Reason)
	}

	rep := &Report{PolicyOverrides: pol.Active()}
	buckets := map[string]Bucket{}
	for _, r := range spec.GetRequirements() {
		e := get(r.GetId())
		bound := len(e.reasons) > 0 || e.witness() || e.static || e.stale || e.broken || e.attested || hasAnyBinding(vr, r.GetId())
		min, overridden := pol.minimum(r.GetKind(), r.GetKeyword())
		exemptCell := overridden && min == stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_EXEMPT
		var b Bucket
		switch {
		case exemptCell && !bound:
			b = Exempt
		case !overridden && r.GetKeyword() == stipulatorv1.Keyword_KEYWORD_MAY && !bound:
			b = Exempt
		case e.broken:
			b = Broken
		case e.stale:
			b = Stale
		case satisfied(pol, r.GetKind(), r.GetKeyword(), e):
			b = Covered
		case e.attested && admitsAttestation(overridden, min, r.GetKeyword()):
			b = Attested
			for _, ar := range e.attestReasons {
				e.reasons = append(e.reasons, "attested: "+ar)
			}
		default:
			b = Uncovered
			e.reasons = append(e.reasons, requiredEvidence(pol, r.GetKind(), r.GetKeyword()))
			for _, ar := range e.attestReasons {
				e.reasons = append(e.reasons, fmt.Sprintf("attestation recorded (%q) does not meet this cell's minimum", ar))
			}
		}
		buckets[r.GetId()] = b
		sort.Strings(e.reasons)
		rep.Requirements = append(rep.Requirements, Requirement{
			Id: r.GetId(), Kind: r.GetKind(), Keyword: r.GetKeyword(),
			Bucket: b, Reasons: e.reasons,
		})
	}

	gapped := map[string]bool{}
	for _, gf := range store.Gaps {
		id := gf.Gap.GetRequirementId()
		gapped[id] = true
		state := Open
		switch {
		case buckets[id] == Covered && !manualUnfired(gf.Gap.GetLands()):
			state = Resolved
		// Coverage is not a state an exempt cell can reach, so the landing
		// condition alone defines completion — without this arm the record
		// would have no reachable terminal state (REQ-gap-lifecycle).
		case buckets[id] == Exempt && conditionHolds(gf.Gap.GetLands(), buckets, spec):
			state = Resolved
		case conditionHolds(gf.Gap.GetLands(), buckets, spec):
			state = Due
		}
		rep.Gaps = append(rep.Gaps, Gap{Path: gf.Path, RequirementId: id, State: state})
	}

	for _, r := range rep.Requirements {
		red := r.Bucket == Uncovered || r.Bucket == Stale || r.Bucket == Broken
		if red && !gapped[r.Id] {
			rep.Violations = append(rep.Violations, r.Id)
		}
	}
	sort.Strings(rep.Violations)
	return rep
}

func hasAnyBinding(vr *verify.Report, id string) bool {
	for _, r := range vr.Results {
		if r.RequirementId == id {
			return true
		}
	}
	return false
}

// admitsAttestation reports whether the effective policy accepts an
// attestation as the cell's minimum: a manifest ATTESTATION cell, or the
// default table's SHOULD/SHOULD NOT row ("a static binding or an
// attestation", REQ-coverage-policy-default). Admission renders the
// distinct attested bucket, never covered.
func admitsAttestation(overridden bool, min stipulatorv1.MinimumEvidence, kw stipulatorv1.Keyword) bool {
	if overridden {
		return min == stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_ATTESTATION
	}
	return kw == stipulatorv1.Keyword_KEYWORD_SHOULD || kw == stipulatorv1.Keyword_KEYWORD_SHOULD_NOT
}

// satisfied applies the effective policy: a manifest override's
// satisfaction set when the cell is named, the default table otherwise.
func satisfied(pol *Policy, kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword, e *evidence) bool {
	if min, ok := pol.minimum(kind, kw); ok {
		switch min {
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_ANALYZER_PROOF:
			return e.proof
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_PROPERTY:
			return e.property || e.proof
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_WITNESS:
			return e.witness()
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_PROOF_OR_WITNESS:
			return e.proof || e.witness()
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_STATIC:
			return e.static || e.witness() || e.proof
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_EXEMPT:
			return true
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_ATTESTATION:
			// Stronger evidence satisfies as covered; attestation alone
			// renders the distinct attested bucket in Evaluate, never
			// covered — deliberately not satisfied here.
			return e.static || e.witness() || e.proof
		}
		return false
	}
	switch kw {
	case stipulatorv1.Keyword_KEYWORD_MUST, stipulatorv1.Keyword_KEYWORD_MUST_NOT:
		switch kind {
		case stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR:
			return e.witness()
		case stipulatorv1.ClauseKind_CLAUSE_KIND_INVARIANT:
			// A for-all claim wants a for-all witness: examples pin
			// anchor cases but never satisfy an invariant alone.
			return e.property || e.proof
		case stipulatorv1.ClauseKind_CLAUSE_KIND_STRUCTURAL:
			return e.proof
		case stipulatorv1.ClauseKind_CLAUSE_KIND_WIRE:
			return e.proof || e.witness()
		}
	case stipulatorv1.Keyword_KEYWORD_SHOULD, stipulatorv1.Keyword_KEYWORD_SHOULD_NOT:
		return e.static || e.witness() || e.proof
	case stipulatorv1.Keyword_KEYWORD_MAY:
		return e.static || e.witness() || e.proof
	}
	return false
}

func requiredEvidence(pol *Policy, kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) string {
	k := strings.ToLower(strings.TrimPrefix(kind.String(), "CLAUSE_KIND_"))
	if min, ok := pol.minimum(kind, kw); ok {
		need := "evidence"
		switch min {
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_ANALYZER_PROOF:
			need = "an analyzer proof"
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_PROPERTY:
			need = "a property witness or analyzer proof"
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_WITNESS:
			need = "an executed witness"
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_PROOF_OR_WITNESS:
			need = "an analyzer proof or witness"
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_STATIC:
			need = "a static binding"
		case stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_ATTESTATION:
			need = "an attestation or stronger"
		}
		return "needs " + need + " (" + k + ", manifest override)"
	}
	switch kw {
	case stipulatorv1.Keyword_KEYWORD_MUST, stipulatorv1.Keyword_KEYWORD_MUST_NOT:
		switch kind {
		case stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR:
			return "needs an executed witness (" + k + ")"
		case stipulatorv1.ClauseKind_CLAUSE_KIND_INVARIANT:
			return "needs a property witness or analyzer proof (" + k + ")"
		case stipulatorv1.ClauseKind_CLAUSE_KIND_STRUCTURAL:
			return "needs an analyzer proof (" + k + ")"
		case stipulatorv1.ClauseKind_CLAUSE_KIND_WIRE:
			return "needs an analyzer proof or witness (" + k + ")"
		}
	}
	if kw == stipulatorv1.Keyword_KEYWORD_SHOULD || kw == stipulatorv1.Keyword_KEYWORD_SHOULD_NOT {
		return "needs a static binding or attestation (" + k + ")"
	}
	return "needs a static binding (" + k + ")"
}

// manualUnfired reports whether the landing condition is a manual
// judgment not yet explicitly fired. Such a gap never resolves on
// coverage alone: a manual condition is an external judgment coverage
// cannot make, so the record stays open on a covered requirement — a
// declared violation that outlives green witnesses (REQ-gap-lifecycle).
// Machine conditions carry no such consent: their firing is
// coverage-shaped, so coverage resolves them as satisfied.
func manualUnfired(lc *stipulatorv1.LandingCondition) bool {
	return lc.HasManual() && !lc.GetManual().GetFired()
}

// conditionHolds evaluates a machine landing condition; manual
// conditions hold only when explicitly fired.
func conditionHolds(lc *stipulatorv1.LandingCondition, buckets map[string]Bucket, spec *stipulatorv1.Spec) bool {
	switch {
	case lc == nil:
		return false
	case lc.HasCovered():
		return buckets[lc.GetCovered()] == Covered
	case lc.HasExists():
		for _, r := range spec.GetRequirements() {
			if r.GetId() == lc.GetExists() {
				return true
			}
		}
		return false
	case lc.HasManual():
		return lc.GetManual().GetFired()
	}
	return false
}
