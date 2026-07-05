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
	// Resolved: the gap's requirement is covered; the record awaits
	// pruning.
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
	// Violations lists red requirements no gap names: the gate fails
	// exactly when this is non-empty.
	Violations []string
}

// GatePasses reports the gate verdict.
func (r *Report) GatePasses() bool { return len(r.Violations) == 0 }

// evidence is what the verifier's facts grant one requirement. proof is
// never granted yet — no analyzer prover exists — but the policy's proof
// legs are contract (REQ-coverage-policy-default), not dead code.
type evidence struct {
	witness, static, proof bool
	broken, stale          bool
	reasons                []string
}

// Evaluate computes buckets, gap states, and the gate verdict. witnessed
// states whether the verify run executed tests: without witnesses, no
// witness-tier evidence exists.
func Evaluate(spec *stipulatorv1.Spec, vr *verify.Report, store *records.Store, witnessed bool) *Report {
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
		if r.Role == stipulatorv1.BindingRole_BINDING_ROLE_TESTS && witnessed {
			switch r.TestOutcome {
			case verify.TestPassed:
				if r.ContentPinned && r.Resolution == verify.Resolved {
					e.witness = true
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

	rep := &Report{}
	buckets := map[string]Bucket{}
	for _, r := range spec.GetRequirements() {
		e := get(r.GetId())
		bound := len(e.reasons) > 0 || e.witness || e.static || e.stale || e.broken || hasAnyBinding(vr, r.GetId())
		var b Bucket
		switch {
		case r.GetKeyword() == stipulatorv1.Keyword_KEYWORD_MAY && !bound:
			b = Exempt
		case e.broken:
			b = Broken
		case e.stale:
			b = Stale
		case satisfied(r.GetKind(), r.GetKeyword(), e):
			b = Covered
		default:
			b = Uncovered
			e.reasons = append(e.reasons, requiredEvidence(r.GetKind(), r.GetKeyword()))
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
		case buckets[id] == Covered:
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

// satisfied applies the default policy: the minimum evidence per (clause
// kind, keyword).
func satisfied(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword, e *evidence) bool {
	switch kw {
	case stipulatorv1.Keyword_KEYWORD_MUST, stipulatorv1.Keyword_KEYWORD_MUST_NOT:
		switch kind {
		case stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR:
			return e.witness
		case stipulatorv1.ClauseKind_CLAUSE_KIND_INVARIANT:
			return e.witness || e.proof
		case stipulatorv1.ClauseKind_CLAUSE_KIND_STRUCTURAL:
			return e.proof
		case stipulatorv1.ClauseKind_CLAUSE_KIND_WIRE:
			return e.proof || e.witness
		}
	case stipulatorv1.Keyword_KEYWORD_SHOULD, stipulatorv1.Keyword_KEYWORD_SHOULD_NOT:
		return e.static || e.witness || e.proof
	case stipulatorv1.Keyword_KEYWORD_MAY:
		return e.static || e.witness || e.proof
	}
	return false
}

func requiredEvidence(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword) string {
	k := strings.ToLower(strings.TrimPrefix(kind.String(), "CLAUSE_KIND_"))
	switch kw {
	case stipulatorv1.Keyword_KEYWORD_MUST, stipulatorv1.Keyword_KEYWORD_MUST_NOT:
		switch kind {
		case stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR:
			return "needs an executed witness (" + k + ")"
		case stipulatorv1.ClauseKind_CLAUSE_KIND_INVARIANT:
			return "needs a witness or analyzer proof (" + k + ")"
		case stipulatorv1.ClauseKind_CLAUSE_KIND_STRUCTURAL:
			return "needs an analyzer proof (" + k + ")"
		case stipulatorv1.ClauseKind_CLAUSE_KIND_WIRE:
			return "needs an analyzer proof or witness (" + k + ")"
		}
	}
	return "needs a static binding (" + k + ")"
}

// conditionHolds evaluates a machine landing condition; attested
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
	case lc.HasAttested():
		return lc.GetAttested().GetFired()
	}
	return false
}
