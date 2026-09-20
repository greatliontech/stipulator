package records

import (
	"fmt"
	"strconv"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/profile"
)

// SetClause scopes a binding to the clause the text names: all digits is
// an ordinal (1-based), a clause label otherwise; empty leaves the
// binding unscoped (the whole requirement). The two forms share one
// oneof, so a binding never carries both.
func SetClause(b *stipulatorv1.Binding, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		b.ClearClause()
		return nil
	}
	if n, err := strconv.ParseUint(text, 10, 32); err == nil {
		if n == 0 {
			return fmt.Errorf("clause ordinal 0: clauses count from 1")
		}
		b.SetClauseOrdinal(uint32(n))
		return nil
	}
	if !profile.ValidClauseLabel(text) {
		return fmt.Errorf("clause %q is neither an ordinal nor a label (%s)", text, profile.ClauseLabelPattern)
	}
	b.SetClauseLabel(text)
	return nil
}

// clauseKey is the clause part of a binding's identity as the claim
// spells it: the empty string for an unscoped claim, "#<ordinal>" for
// an ordinal, the label in backticks for a label — a present label,
// empty included, never reads as the unscoped claim. Two bindings
// differing only here are two claims.
func clauseKey(b *stipulatorv1.Binding) string {
	switch {
	case b.HasClauseOrdinal():
		return "#" + strconv.FormatUint(uint64(b.GetClauseOrdinal()), 10)
	case b.HasClauseLabel():
		return "`" + b.GetClauseLabel() + "`"
	}
	return ""
}

// ClauseSpelling is the clause exactly as the claim spells it — the
// value `--clause` takes: "3" for an ordinal, the label for a label,
// empty for a whole-requirement claim.
func ClauseSpelling(b *stipulatorv1.Binding) string {
	switch {
	case b.HasClauseOrdinal():
		return strconv.FormatUint(uint64(b.GetClauseOrdinal()), 10)
	case b.HasClauseLabel():
		return b.GetClauseLabel()
	}
	return ""
}

// ClauseName renders the clause a binding names for messages: "clause 3"
// or "clause `label`"; empty for an unscoped claim.
func ClauseName(b *stipulatorv1.Binding) string {
	switch {
	case b.HasClauseOrdinal():
		return fmt.Sprintf("clause %d", b.GetClauseOrdinal())
	case b.HasClauseLabel():
		return fmt.Sprintf("clause `%s`", b.GetClauseLabel())
	}
	return ""
}

// ClaimClauseKey is the clause part of a claim's identity against a
// requirement: the resolved clause's ordinal when the claim resolves —
// so one clause named by ordinal and by label is one claim — and the
// spelling otherwise (a dangling claim is malformed and judged on its
// own); empty for a whole-requirement claim.
func ClaimClauseKey(req *stipulatorv1.Requirement, b *stipulatorv1.Binding) string {
	if c, ok := ResolveClause(req, b); ok {
		if c == nil {
			return ""
		}
		return "#" + strconv.FormatUint(uint64(c.GetOrdinal()), 10)
	}
	return clauseKey(b)
}

// ClauseSuffix renders a binding's clause for a message, with a
// leading space — as the corpus resolves it when it does, as the claim
// spells it otherwise (req may be nil where no corpus is loaded);
// empty for a whole-requirement claim.
func ClauseSuffix(req *stipulatorv1.Requirement, b *stipulatorv1.Binding) string {
	if c, ok := ResolveClause(req, b); ok && c != nil {
		return " " + ClauseHeading(c)
	}
	if name := ClauseName(b); name != "" {
		return " " + name
	}
	return ""
}

// ClaimIdentity is a claim's whole identity — requirement, backend,
// symbol, role, and the resolved clause (REQ-evidence-clause-claim) —
// the one spelling every duplicate judgment reads: verification's
// hygiene and the retarget's collision check. req may be nil where no
// corpus is loaded: the clause part is then the claim's own spelling.
func ClaimIdentity(req *stipulatorv1.Requirement, b *stipulatorv1.Binding) string {
	return ClaimIdentityAt(req, b, b.GetSymbol())
}

// ClaimIdentityAt is ClaimIdentity with the symbol replaced — the
// identity a rewrite would give the claim.
func ClaimIdentityAt(req *stipulatorv1.Requirement, b *stipulatorv1.Binding, symbol string) string {
	return b.GetRequirementId() + "|" + b.GetBackend() + "|" + symbol + "|" + b.GetRole().String() + "|" + ClaimClauseKey(req, b)
}

// ResolveClause finds the clause a binding names among a requirement's
// clauses. ok is false when the binding names one the requirement does
// not declare; an unscoped binding resolves to nil, true.
func ResolveClause(req *stipulatorv1.Requirement, b *stipulatorv1.Binding) (*stipulatorv1.Clause, bool) {
	switch {
	case b.HasClauseOrdinal():
		for _, c := range req.GetClauses() {
			if c.GetOrdinal() == b.GetClauseOrdinal() {
				return c, true
			}
		}
		return nil, false
	case b.HasClauseLabel():
		// An empty label names no clause: a requirement's unlabeled
		// clauses are reached by ordinal alone.
		if b.GetClauseLabel() == "" {
			return nil, false
		}
		for _, c := range req.GetClauses() {
			if c.GetLabel() == b.GetClauseLabel() {
				return c, true
			}
		}
		return nil, false
	}
	return nil, true
}

// ClauseHead is the one spelling of a RESOLVED clause's name: "clause 3
// `label`" or "clause 3" — every report row and heading that names a
// clause the corpus resolved reads it (a binding's own unresolved
// spelling is ClauseName's).
func ClauseHead(c *stipulatorv1.Clause) string {
	head := fmt.Sprintf("clause %d", c.GetOrdinal())
	if c.GetLabel() != "" {
		head += " `" + c.GetLabel() + "`"
	}
	return head
}

// ClauseHeading renders a clause for reports: its head followed by a
// bounded prefix of its text.
func ClauseHeading(c *stipulatorv1.Clause) string {
	return ClauseHead(c) + " (" + textPrefix(c.GetText(), 60) + ")"
}

// ClausesOffered renders what a requirement offers a clause claim: its
// clauses by ordinal and label, or the fact that it has none.
func ClausesOffered(r *stipulatorv1.Requirement) string {
	if len(r.GetClauses()) == 0 {
		return "the requirement has no payload list, so it takes only whole-requirement claims"
	}
	parts := make([]string, 0, len(r.GetClauses()))
	for _, c := range r.GetClauses() {
		parts = append(parts, ClauseHeading(c))
	}
	return "its clauses are " + strings.Join(parts, "; ")
}

func textPrefix(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
