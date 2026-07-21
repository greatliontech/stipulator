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
		open, prunable := coverage.GapCounts(cov.Gaps, keptIDs)
		out.SetGapsOpen(int32(open))
		out.SetResolvedGapsPrunable(int32(prunable))
		// The trust settlement: an override shapes the verdict and every
		// count, so even the roll-up surfaces it, never applies it
		// silently.
		out.SetPolicyOverrides(cov.PolicyOverrides)
		return out, nil
	case "reds":
		var red []coverage.Requirement
		for _, r := range kept {
			switch r.Bucket {
			case coverage.Uncovered, coverage.Stale, coverage.Broken:
				red = append(red, r)
			}
		}
		return coverageReportProto(cov, red, scopeKeep(scope, keptIDs)), nil
	case "full":
		return coverageReportProto(cov, kept, scopeKeep(scope, keptIDs)), nil
	}
	return nil, fmt.Errorf("unknown view %q (summary, reds, full)", view)
}

// scopeKeep returns the requirement-id set to narrow a report's gaps and
// violations to, or nil for an unscoped call — where the whole report
// passes through, so an orphan gap (one naming a requirement absent from
// the corpus) is still emitted rather than silently dropped.
func scopeKeep(scope Scope, keptIDs map[string]bool) map[string]bool {
	if scope.Empty() {
		return nil
	}
	return keptIDs
}

// ScopeReport returns a copy of cov narrowed to a scope: Requirements set
// to rows, and — when keep is non-nil — its Gaps and Violations filtered to
// the kept requirement ids, so a scoped view is not polluted by out-of-scope
// entries. keep==nil leaves gaps and violations untouched (an unscoped
// call). The gate verdict is deliberately NOT re-derived here: a scoped
// slice with no in-scope violation says nothing about whether the tree
// passes, so callers keep the global GatePasses().
func ScopeReport(cov *coverage.Report, rows []coverage.Requirement, keep map[string]bool) coverage.Report {
	sliced := *cov
	sliced.Requirements = rows
	if keep != nil {
		gaps := make([]coverage.Gap, 0, len(cov.Gaps))
		for _, g := range cov.Gaps {
			if keep[g.RequirementId] {
				gaps = append(gaps, g)
			}
		}
		sliced.Gaps = gaps
		viol := make([]string, 0, len(cov.Violations))
		for _, v := range cov.Violations {
			if keep[v] {
				viol = append(viol, v)
			}
		}
		sliced.Violations = viol
	}
	return sliced
}

// coverageReportProto renders the report wire message with the rows
// replaced by the given slice and gaps/violations narrowed to the scope
// (keep); the gate verdict it carries stays the GLOBAL one.
func coverageReportProto(cov *coverage.Report, rows []coverage.Requirement, keep map[string]bool) *stipulatorv1.CoverageReport {
	sliced := ScopeReport(cov, rows, keep)
	out := sliced.Proto()
	out.SetGatePasses(cov.GatePasses())
	return out
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
		out.SetOutsidePolicy(int32(vr.OutsidePolicy))
		// Failure-diagnostic bodies are runtime products the summary
		// omits (REQ-mcp-response-contract): it carries capped heading
		// words with the omitted remainder counted, and the typed rows
		// ride the full result for drill-down.
		var headings []string
		for _, d := range vr.Diagnostics {
			headings = append(headings, diagnosticHeadingWord(d))
		}
		if len(headings) > headingCap {
			out.SetWitnessFailureHeadingsOmitted(int32(len(headings) - headingCap))
			headings = headings[:headingCap]
		}
		out.SetWitnessFailureHeadings(headings)
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
			// A scope narrows the WHOLE report (REQ-mcp-views): the
			// typed diagnostics follow the kept rows, so filtered triage
			// is never polluted by out-of-scope packages' failures.
			// OutsidePolicy stays GLOBAL exactly like the gate verdict — a
			// scoped slice says nothing about what the policy leaves
			// outside the tree-wide witnessing.
			var keptDiags []*stipulatorv1.FailureDiagnostic
			for _, d := range vr.Diagnostics {
				// A path scope keeps a package-scoped diagnostic
				// directly, row or no row — the same raw-prefix rule
				// keeps applies to symbols. Without it a build-broken
				// package would lose the one diagnostic explaining its
				// breakage exactly when scoped onto: its rows resolve
				// to no package. An invocation-level diagnostic has an
				// empty package, which no non-empty path prefixes. A
				// Path-empty scope (ids, filter, bucket) has nothing to
				// rescue with, so a broken package's diagnostic drops
				// from those scoped views; the unscoped view, summary
				// headings, and check-level rows still carry it.
				if scope.Path != "" && strings.HasPrefix(d.GetPackage(), scope.Path) {
					keptDiags = append(keptDiags, d)
					continue
				}
				for _, br := range rows {
					// Match on the row's backend-resolved package — the
					// symbol string alone is ambiguous (dotted path
					// elements vs method receivers), so it is never
					// re-parsed here. A row without a resolved package
					// identifies no package and claims no diagnostic.
					if br.Package != "" && br.Package == d.GetPackage() {
						keptDiags = append(keptDiags, d)
						break
					}
				}
			}
			sliced.Diagnostics = keptDiags
		}
		return sliced.Proto(), nil
	}
	return nil, fmt.Errorf("unknown view %q (summary, bindings)", view)
}
