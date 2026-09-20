package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/stipulate"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func lineWith(out, id string) string {
	for _, l := range strings.Split(ansi.ReplaceAllString(out, ""), "\n") {
		if strings.Contains(l, id) {
			return l
		}
	}
	return ""
}

// The human check rendering prints every red row the one ladder
// classified and marks the class of a row restating a result-level
// cause — policy-blocked under a fired witness-selection diagnostic,
// scope-blocked on a scoped pass — while a red of its own carries no
// mark, and no mark appears when the result never raised the cause.
func TestCheckRenderMarksFoldedRedRows(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	row := func(id string, policy, scope bool) *stipulatorv1.RequirementCoverage {
		r := &stipulatorv1.RequirementCoverage{}
		r.SetId(id)
		r.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
		r.SetReasons([]string{id + " reason"})
		r.SetWitnessSelectionBlocked(policy)
		r.SetScopeBlocked(scope)
		return r
	}
	cov := &stipulatorv1.CoverageReport{}
	cov.SetRequirements([]*stipulatorv1.RequirementCoverage{row("REQ-policy", true, false), row("REQ-scope", false, true), row("REQ-plain", false, false)})
	cov.SetViolations([]string{"REQ-policy", "REQ-scope", "REQ-plain"})
	res := &stipulatorv1.CheckResult{}
	res.SetCoverage(cov)
	res.SetWitnessSelectionProblem("no expected witness")
	res.SetScopePartial(true)
	var stdout, stderr bytes.Buffer
	renderCheck(&stdout, &stderr, res)
	out := stdout.String()
	// Among the violations only the scope-blocked row is the scoped
	// pass's exclusion; a policy-blocked row is a violation the verdict
	// counts, rendered as one.
	errs := ansi.ReplaceAllString(stderr.String(), "")
	if l := lineWith(errs, "REQ-scope"); !strings.HasPrefix(l, "scope-blocked: ") {
		t.Errorf("scope-blocked violation line: %q", l)
	}
	for _, id := range []string{"REQ-policy", "REQ-plain"} {
		if l := lineWith(errs, id); !strings.HasPrefix(l, "violation: ") {
			t.Errorf("%s violation line: %q", id, l)
		}
	}
	if l := lineWith(out, "REQ-policy"); !strings.Contains(l, "broken") || !strings.Contains(l, "(policy-blocked)") {
		t.Errorf("policy-blocked row unmarked: %q", l)
	}
	if l := lineWith(out, "REQ-scope"); !strings.Contains(l, "(scope-blocked)") {
		t.Errorf("scope-blocked row unmarked: %q", l)
	}
	if l := lineWith(out, "REQ-plain"); l == "" || strings.Contains(l, "-blocked)") {
		t.Errorf("a red of its own carries a mark or is missing: %q", l)
	}

	if !strings.Contains(stderr.String(), "no expected witness") {
		t.Errorf("the cause the mark points at is not stated:\n%s", stderr.String())
	}
	// The health-judged form states the cause too: a mark with no cause
	// line is a dangling reference.
	res.SetExecution(&stipulatorv1.ExecutionReport{})
	res.SetSuiteHealthJudged(true)
	stdout.Reset()
	stderr.Reset()
	renderCheck(&stdout, &stderr, res)
	if !strings.Contains(stderr.String(), "no expected witness") || !strings.Contains(lineWith(stdout.String(), "REQ-policy"), "(policy-blocked)") {
		t.Errorf("health-judged form: cause line and mark:\n%s\n%s", stderr.String(), stdout.String())
	}

	res.SetExecution(nil)
	res.SetSuiteHealthJudged(false)
	res.SetWitnessSelectionProblem("")
	res.SetScopePartial(false)
	stdout.Reset()
	renderCheck(&stdout, &stderr, res)
	if out := ansi.ReplaceAllString(stdout.String(), ""); strings.Contains(out, "-blocked)") || !strings.Contains(out, "REQ-policy") {
		t.Errorf("with no result-level cause every row is a plain red:\n%s", out)
	}
}

// The gate's rows are the red set every surface consults plus the
// attested rows, which appear distinctly in every coverage output; a
// covered or exempt requirement never lists. Its summary line counts
// gaps in the one tally grammar, every count distinct so a swapped
// state changes the line.
func TestGateRowsAreTheRedSetAndAttested(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-attestation")
	stipulate.Covers(t, "REQ-gap-lifecycle")
	cov := &coverage.Report{
		Requirements: []coverage.Requirement{
			{Id: "REQ-cov", Bucket: coverage.Covered},
			{Id: "REQ-att", Bucket: coverage.Attested},
			{Id: "REQ-ex", Bucket: coverage.Exempt},
			{Id: "REQ-part", Bucket: coverage.Partial, Reasons: []string{"clause 2 unclaimed"}},
			{Id: "REQ-unc", Bucket: coverage.Uncovered},
		},
		Gaps: []coverage.Gap{
			{RequirementId: "REQ-a", State: coverage.Open, Contradicted: true},
			{RequirementId: "REQ-b", State: coverage.Open, Contradicted: true},
			{RequirementId: "REQ-c", State: coverage.Open},
			{RequirementId: "REQ-d", State: coverage.Due, Contradicted: true},
			{RequirementId: "REQ-e", State: coverage.Due, Contradicted: true},
			{RequirementId: "REQ-f", State: coverage.Resolved, Contradicted: true},
		},
	}
	var buf bytes.Buffer
	printCoverage(&buf, cov)
	out := ansi.ReplaceAllString(buf.String(), "")
	for _, id := range []string{"REQ-att", "REQ-part", "REQ-unc"} {
		if lineWith(out, id) == "" {
			t.Errorf("%s not listed:\n%s", id, out)
		}
	}
	for _, id := range []string{"REQ-cov", "REQ-ex"} {
		if lineWith(out, id) != "" {
			t.Errorf("%s listed:\n%s", id, out)
		}
	}
	if !strings.Contains(out, "gaps: 3 open, 2 due, 1 resolved (4 of the unresolved contradicted)") {
		t.Errorf("gate line lacks the one tally grammar:\n%s", out)
	}
}
