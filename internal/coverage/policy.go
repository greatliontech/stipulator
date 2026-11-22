package coverage

import (
	"fmt"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Policy resolves each (clause kind, keyword) cell to its minimum
// evidence: manifest overrides win over the default table
// (REQ-coverage-policy). A nil Policy is the pure default table.
type Policy struct {
	overrides              map[policyCell]stipulatorv1.MinimumEvidence
	allowWholeClauseClaims bool
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
	p := &Policy{
		overrides:              map[policyCell]stipulatorv1.MinimumEvidence{},
		allowWholeClauseClaims: m.GetAllowWholeClauseClaims(),
	}
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
	if p.allowWholeClauseClaims {
		out = append(out, "policy override: allow_whole_clause_claims -> true")
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

// Overridden reports whether the manifest overrides the cell's minimum
// — the predicate the kind hint is suppressed on, for a surface
// composing the hint outside this package.
func (p *Policy) Overridden(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) bool {
	_, ok := p.minimum(kind, kw)
	return ok
}

// minimum reports the overridden minimum for a cell, if any.
func (p *Policy) minimum(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) (stipulatorv1.MinimumEvidence, bool) {
	if p == nil {
		return stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_UNSPECIFIED, false
	}
	m, ok := p.overrides[policyCell{kind, kw}]
	return m, ok
}

// AdmitsAttestation reports whether the (kind, keyword) cell can ever
// render the attested bucket under pol — the born-valid check the
// attest verb applies at write time: an attestation that can never
// render is refused, not recorded.
func AdmitsAttestation(pol *Policy, kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) bool {
	min, overridden := pol.minimum(kind, kw)
	return admitsAttestation(overridden, min, kw)
}

// RequiredEvidence names the cell's evidence demand, human-readably.
func RequiredEvidence(pol *Policy, kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) string {
	return requiredEvidence(pol, kind, kw)
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

// KindHint states, beside an invariant cell's need, what the kinds
// mean — so an author whose requirement enumerates a closed set rather
// than stating a for-all property is pointed at the classification the
// need follows from, and never left to hunt for a gap-shaped excuse.
// It states the taxonomy (the clause kind definitions) and prescribes
// nothing: which kind a requirement is remains the author's judgment
// (REQ-change-remediation). Empty for every other kind.
func KindHint(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) string {
	if kind != stipulatorv1.ClauseKind_CLAUSE_KIND_INVARIANT {
		return ""
	}
	// Only a MUST cell's need follows from the kind; SHOULD and MAY
	// needs follow from the keyword alone.
	if kw != stipulatorv1.Keyword_KEYWORD_MUST && kw != stipulatorv1.Keyword_KEYWORD_MUST_NOT {
		return ""
	}
	return "the need follows from the kind: an invariant is a property over all reachable states or runs; a closed set observable across the wire or a restart (an encoding, a format, a served inventory) is a wire requirement; dependency direction, boundaries, and interface satisfaction are structural"
}

func requiredEvidence(pol *Policy, kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) string {
	k := kindName(kind)
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

// ConditionText renders a landing condition for the triage surfaces:
// "covered(<id>)", "exists(<id>)", or "manual: <text>".
func ConditionText(lc *stipulatorv1.LandingCondition) string {
	switch {
	case lc.GetCovered() != "":
		return "covered(" + lc.GetCovered() + ")"
	case lc.GetExists() != "":
		return "exists(" + lc.GetExists() + ")"
	case lc.GetManual() != nil:
		return "manual: " + lc.GetManual().GetCondition()
	}
	return ""
}

// conditionHolds evaluates a machine landing condition; manual
// conditions hold only when explicitly fired.
func conditionHolds(lc *stipulatorv1.LandingCondition, buckets map[string]Bucket, corpus records.Hashes) bool {
	switch {
	case lc == nil:
		return false
	case lc.HasCovered():
		return buckets[lc.GetCovered()] == Covered
	case lc.HasExists():
		return corpus.Known(lc.GetExists())
	case lc.HasManual():
		return lc.GetManual().GetFired()
	}
	return false
}
