package verify

import (
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
)

// The record hygiene: the static judgment of every record against the
// corpus, before any witness runs.

// Hygiene judges the record-only half of verification — every problem
// the records carry against the compiled corpus alone: duplicate,
// malformed, or out-of-corpus bindings; attestations without a
// requirement or reason, duplicated, out of the corpus, or
// contradicting a gap; gaps without a requirement, reason, or landing
// condition, duplicated, or out of the corpus. It needs no backend and no witness, so an
// operation refuses on it before any child process
// (REQ-check-preparation); Run reports the same problems from the same
// judgment, beside the resolved rows.
func Hygiene(spec *stipulatorv1.Spec, store *records.Store) []Problem {
	judge := newHygiene(spec, store)
	var out []Problem
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			problems, _, _ := judge.binding(bf.Path, b)
			out = append(out, problems...)
		}
	}
	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			problems, _ := judge.attestation(af.Path, a)
			out = append(out, problems...)
		}
	}
	for _, gf := range store.Gaps {
		out = append(out, judge.gap(gf.Path, gf.Gap)...)
	}
	sortProblems(out)
	return out
}

// sortProblems orders problems by path then message, the one order
// every reporter renders.
func sortProblems(problems []Problem) {
	sort.Slice(problems, func(i, j int) bool {
		a, b := problems[i], problems[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})
}

// hygiene is the record-only judgment, one instance per pass: it
// remembers every claim seen so a duplicate is named on its second
// appearance, and the gapped requirements so an attestation
// contradicting a gap is named.
type hygiene struct {
	hashes   records.Hashes
	reqs     map[string]*stipulatorv1.Requirement
	seen     map[string]bool
	gapped   map[string]bool
	attested map[string]string
	seenGaps map[string]string
}

func newHygiene(spec *stipulatorv1.Spec, store *records.Store) *hygiene {
	j := &hygiene{hashes: records.HashesOf(spec), reqs: records.ByID(spec), seen: map[string]bool{}, gapped: map[string]bool{}, attested: map[string]string{}, seenGaps: map[string]string{}}
	for _, gf := range store.Gaps {
		j.gapped[gf.Gap.GetRequirementId()] = true
	}
	return j
}

// binding judges one claim: its problems, whether it is malformed —
// unresolvable by any backend, so verification skips it — and the
// clause the claim resolves to (nil for a whole-requirement claim),
// resolved once here for every consumer.
func (j *hygiene) binding(path string, b *stipulatorv1.Binding) (problems []Problem, malformed bool, clause *stipulatorv1.Clause) {
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	id := b.GetRequirementId()
	// Two claims naming one clause — by ordinal and by label — are one
	// claim: the identity carries the resolved clause, not its spelling.
	key := records.ClaimIdentity(j.reqs[id], b)
	if j.seen[key] {
		// The message names the clause as the corpus resolves it, so a
		// pair spelled by ordinal and by label reads as one clause.
		problem("duplicate binding: %s %s %s%s", id, b.GetSymbol(), b.GetRole(), records.ClauseSuffix(j.reqs[id], b))
	}
	j.seen[key] = true
	if id == "" {
		problem("binding without requirement_id")
		malformed = true
	}
	if b.GetBackend() == "" {
		problem("binding for %s has no backend", id)
		malformed = true
	}
	if b.GetSymbol() == "" {
		problem("binding for %s has no symbol", id)
		malformed = true
	}
	if b.GetRole() == stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
		problem("binding for %s has no role", id)
		malformed = true
	}
	if id != "" && !j.hashes.Known(id) {
		problem("binding names %s, which is not in the corpus — unbind it: %s (or %s if the requirement was removed deliberately)", id, remedy.Unbind(id, "", ""), remedy.Retire(id))
		malformed = true
	} else if id != "" {
		// A clause claim on a clause the requirement no longer declares
		// is a dangling record exactly as an out-of-corpus id is: it can
		// grant nothing and must not vanish into an uncovered row
		// (REQ-evidence-clause-claim).
		var ok bool
		if clause, ok = records.ResolveClause(j.reqs[id], b); !ok {
			problem("binding %s on %s names %s, which %s no longer declares — rebind against its current clauses or unbind it: %s", b.GetSymbol(), id, records.ClauseName(b), id, remedy.Unbind(id, b.GetSymbol(), records.ClauseSpelling(b)))
			malformed = true
		}
	}
	return problems, malformed, clause
}

// attestation judges one judgment record: its problems, and whether it
// stands — a standing attestation is the one judgment for its
// requirement.
func (j *hygiene) attestation(path string, a *stipulatorv1.RequirementAttestation) (problems []Problem, stands bool) {
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	id := a.GetRequirementId()
	switch {
	case id == "":
		problem("attestation without requirement_id")
		return problems, false
	case a.GetReason() == "":
		problem("attestation for %s has no reason", id)
		return problems, false
	}
	if prior, dup := j.attested[id]; dup {
		problem("attestation for %s duplicates %s; one judgment per requirement", id, prior)
		return problems, false
	}
	j.attested[id] = path
	if !j.hashes.Known(id) {
		problem("attestation names %s, which is not in the corpus — retract it: %s", id, remedy.AttestRetract(id))
		return problems, false
	}
	if j.gapped[id] {
		// Deferred and judged-satisfied contradict: the records cannot
		// both stand.
		problem("%s is both gapped and attested; the records contradict — retract one: %s, or %s", id, remedy.GapRetract(id), remedy.AttestRetract(id))
		return problems, false
	}
	return problems, true
}

// gap judges one deferral record: without a requirement, reason, or
// landing condition, duplicated, or naming a requirement the corpus does
// not have. Landing-condition targets are deliberately not resolved:
// exists(...) and covered(...) may name requirements the spec does not
// hold yet — that prospectiveness is their purpose.
func (j *hygiene) gap(path string, g *stipulatorv1.Gap) (problems []Problem) {
	problem := func(format string, args ...any) {
		problems = append(problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	id := g.GetRequirementId()
	if id == "" {
		problem("gap without requirement_id")
	} else if !j.hashes.Known(id) {
		problem("gap names %s, which is not in the corpus — retract it: %s (or %s for the bulk repair)", id, remedy.GapRetract(id), remedy.Prune(true))
	}
	if id != "" {
		if prior, dup := j.seenGaps[id]; dup {
			problem("gap for %s duplicates %s; one declaration per requirement", id, prior)
		} else {
			j.seenGaps[id] = path
		}
	}
	if g.GetReason() == "" {
		problem("gap for %s has no reason", id)
	}
	if !g.HasLands() {
		problem("gap for %s has no landing condition", id)
	}
	return problems
}
