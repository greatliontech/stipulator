// Package views renders the report views the read surfaces share: one
// renderer feeds the MCP tools and the CLI flags, so the surfaces cannot
// drift (REQ-mcp-views). A view picks how much per
// item; a scope picks which items — both are presentation over reports
// computed by their owners, never new facts.
package views

import (
	"fmt"
	"path"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/proto"

	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Scope selects items: exact ids, a bucket name, an id glob (path.Match
// syntax, e.g. REQ-arch-*), and a path prefix matched against the
// requirement's declaring document and its bound symbols.
type Scope struct {
	Ids    []string
	Bucket string
	Filter string
	Path   string
}

// Empty reports whether the scope selects everything.
func (s Scope) Empty() bool {
	return len(s.Ids) == 0 && s.Bucket == "" && s.Filter == "" && s.Path == ""
}

var bucketNames = map[string]coverage.Bucket{
	"uncovered": coverage.Uncovered,
	"stale":     coverage.Stale,
	"broken":    coverage.Broken,
	"covered":   coverage.Covered,
	"exempt":    coverage.Exempt,
	"attested":  coverage.Attested,
}

// Validate refuses unknown scope vocabulary before any filtering happens
// — a typo must never read as an empty result.
func (s Scope) Validate() error {
	if s.Bucket != "" {
		if _, ok := bucketNames[strings.ToLower(s.Bucket)]; !ok {
			return fmt.Errorf("unknown bucket %q (uncovered, stale, broken, covered, exempt, attested)", s.Bucket)
		}
	}
	if s.Filter != "" {
		if _, err := path.Match(s.Filter, "probe"); err != nil {
			return fmt.Errorf("bad filter %q: %w", s.Filter, err)
		}
	}
	return nil
}

// keeps reports whether a requirement passes the scope, given its
// declaring document and bound symbols.
func (s Scope) keeps(row coverage.Requirement, doc string, symbols []string) bool {
	if len(s.Ids) > 0 {
		found := false
		for _, id := range s.Ids {
			if id == row.Id {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if s.Bucket != "" && bucketNames[strings.ToLower(s.Bucket)] != row.Bucket {
		return false
	}
	if s.Filter != "" {
		if ok, _ := path.Match(s.Filter, row.Id); !ok {
			return false
		}
	}
	if s.Path != "" && !strings.HasPrefix(doc, s.Path) && !anyPrefix(symbols, s.Path) {
		return false
	}
	return true
}

func anyPrefix(items []string, prefix string) bool {
	for _, it := range items {
		if strings.HasPrefix(it, prefix) {
			return true
		}
	}
	return false
}

// Facts carries the per-requirement context scoping needs: declaring
// document and bound symbols. Derive with FactsFrom.
type Facts struct {
	Doc     map[string]string
	Symbols map[string][]string
}

// FactsFrom derives scoping facts from the compiled corpus and the
// verification report.
func FactsFrom(spec *stipulatorv1.Spec, vr *verify.Report) Facts {
	f := Facts{Doc: map[string]string{}, Symbols: map[string][]string{}}
	for _, r := range spec.GetRequirements() {
		f.Doc[r.GetId()] = r.GetLocation().GetDocument()
	}
	for _, br := range vr.Results {
		f.Symbols[br.RequirementId] = append(f.Symbols[br.RequirementId], br.Symbol)
	}
	return f
}

// FilterRows applies a scope to the report's requirement rows — the one
// filter both the wire views and the CLI's human rendering use.
func FilterRows(cov *coverage.Report, facts Facts, scope Scope) ([]coverage.Requirement, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	kept := make([]coverage.Requirement, 0, len(cov.Requirements))
	for _, r := range cov.Requirements {
		if scope.keeps(r, facts.Doc[r.Id], facts.Symbols[r.Id]) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// CoverageView renders the coverage report at a view over a scope.
// Views: summary (the roll-up most calls want — counts over the scoped
// rows, violations intersected with the scope, the GLOBAL gate verdict),
// reds (only uncovered, stale, and broken rows), full (every scoped
// row). The empty view means summary.
func CoverageView(cov *coverage.Report, facts Facts, view string, scope Scope) (proto.Message, error) {
	kept, err := FilterRows(cov, facts, scope)
	if err != nil {
		return nil, err
	}
	keptIDs := map[string]bool{}
	for _, r := range kept {
		keptIDs[r.Id] = true
	}
	switch view {
	case "", "summary":
		out := &stipulatorv1.CoverageSummary{}
		// The gate verdict is the global one: a slice passing means
		// nothing when the tree fails.
		out.SetGatePasses(cov.GatePasses())
		var c, a, u, st, b, e int32
		for _, r := range kept {
			switch r.Bucket {
			case coverage.Covered:
				c++
			case coverage.Attested:
				a++
			case coverage.Uncovered:
				u++
			case coverage.Stale:
				st++
			case coverage.Broken:
				b++
			case coverage.Exempt:
				e++
			}
		}
		out.SetCovered(c)
		out.SetAttested(a)
		out.SetUncovered(u)
		out.SetStale(st)
		out.SetBroken(b)
		out.SetExempt(e)
		var viol []string
		for _, v := range cov.Violations {
			if keptIDs[v] {
				viol = append(viol, v)
			}
		}
		out.SetViolations(viol)
		open := int32(0)
		for _, g := range cov.Gaps {
			if g.State != coverage.Resolved && keptIDs[g.RequirementId] {
				open++
			}
		}
		out.SetGapsOpen(open)
		// The trust settlement: an override shapes the verdict and every
		// count, so even the roll-up surfaces it, never applies it
		// silently.
		out.SetPolicyOverrides(cov.PolicyOverrides)
		return out, nil
	case "reds":
		red := kept[:0:0]
		for _, r := range kept {
			switch r.Bucket {
			case coverage.Uncovered, coverage.Stale, coverage.Broken:
				red = append(red, r)
			}
		}
		return coverageReportProto(cov, red), nil
	case "full":
		return coverageReportProto(cov, kept), nil
	}
	return nil, fmt.Errorf("unknown view %q (summary, reds, full)", view)
}

// coverageReportProto renders the report wire message with the rows
// replaced by the given slice — everything else (gaps, violations,
// policy overrides) stays the owner's.
func coverageReportProto(cov *coverage.Report, rows []coverage.Requirement) *stipulatorv1.CoverageReport {
	sliced := *cov
	sliced.Requirements = rows
	return sliced.Proto()
}

// VerifyView renders the verification report at a view: summary (record
// hygiene and witness counts, with change signatures — no per-binding
// rows) or bindings (the full rows, scoped). The empty view means
// summary.
func VerifyView(vr *verify.Report, facts Facts, view string, scope Scope) (proto.Message, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	switch view {
	case "", "summary":
		out := &stipulatorv1.VerifySummary{}
		out.SetProblems(int32(len(vr.Problems)))
		out.SetPinned(int32(vr.Pinned))
		out.SetStale(int32(vr.Stale))
		out.SetShapePinned(int32(vr.ShapePinned))
		out.SetShapeUnpinned(int32(vr.ShapeUnpinned))
		out.SetShapeMismatch(int32(vr.ShapeMismatch))
		out.SetBroken(int32(vr.Broken))
		out.SetUnverified(int32(vr.Unverified))
		out.SetTestsPassed(int32(vr.TestsPassed))
		out.SetTestsFailed(int32(vr.TestsFailed))
		out.SetTestsNotRun(int32(vr.TestsNotRun))
		var sigs []*stipulatorv1.ChangeSignature
		for _, cs := range vr.Signatures {
			m := &stipulatorv1.ChangeSignature{}
			m.SetRequirementId(cs.RequirementId)
			m.SetLabel(verify.LabelProto(cs.Label))
			m.SetEvidence(cs.Evidence)
			sigs = append(sigs, m)
		}
		out.SetSignatures(sigs)
		return out, nil
	case "bindings":
		sliced := *vr
		if !scope.Empty() {
			var rows []verify.BindingResult
			for _, br := range vr.Results {
				row := coverage.Requirement{Id: br.RequirementId}
				if scope.Bucket != "" {
					return nil, fmt.Errorf("bucket scope applies to coverage views, not binding rows")
				}
				if scope.keeps(row, facts.Doc[br.RequirementId], []string{br.Symbol}) {
					rows = append(rows, br)
				}
			}
			sliced.Results = rows
		}
		return sliced.Proto(), nil
	}
	return nil, fmt.Errorf("unknown view %q (summary, bindings)", view)
}

// HardenView renders the harden report at a view: summary (counts plus
// only the OPEN survivors — the ones needing a disposition) or full
// (the records with attestation prose). The empty view means summary.
func HardenView(rep *harden.Report, view string) (proto.Message, error) {
	switch view {
	case "", "summary":
		out := &stipulatorv1.HardenSummary{}
		var results []*stipulatorv1.HardenResultSummary
		for _, res := range rep.Results {
			m := &stipulatorv1.HardenResultSummary{}
			m.SetSymbol(res.Symbol)
			m.SetRequirementIds(res.Requirements)
			m.SetMutants(int32(res.Mutants))
			m.SetKilled(int32(res.Killed))
			m.SetAttested(int32(len(res.Attested)))
			m.SetCached(res.Cached)
			attested := map[string]bool{}
			for _, a := range res.Attested {
				attested[a.Position+"|"+a.Operator] = true
			}
			var open []*stipulatorv1.MutationSurvivor
			for _, s := range res.Survivors {
				if attested[s.Position+"|"+s.Operator] {
					continue
				}
				ms := &stipulatorv1.MutationSurvivor{}
				ms.SetPosition(s.Position)
				ms.SetOperator(s.Operator)
				open = append(open, ms)
			}
			m.SetOpenSurvivors(open)
			results = append(results, m)
		}
		out.SetResults(results)
		return out, nil
	case "full":
		return rep.Proto(), nil
	}
	return nil, fmt.Errorf("unknown view %q (summary, full)", view)
}
