package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/views"
)

// checkIn selects the check's evidence class: full demands suite
// judgment through one whole policy execution; the default serves fresh
// witnesses and selectively executes the stale remainder
// (REQ-check-verdict).
type checkIn struct {
	Full bool   `json:"full,omitempty"`
	View string `json:"view,omitempty"`
	Ids  string `json:"ids,omitempty"`
}

func (s *Server) toolCheck(ctx context.Context, req *mcp.CallToolRequest, in checkIn) (*mcp.CallToolResult, map[string]any, error) {
	// View and scope words are validated before the expensive pass: a
	// typo must not cost a witness run only to be refused at render time.
	ids, err := splitIDsLoose(in.Ids)
	if err != nil {
		return nil, nil, err
	}
	if _, err := views.CheckView(&stipulatorv1.CheckResult{}, in.View, nil); err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	res, err := s.runCheck(ctx, in.Full, ids)
	if err != nil {
		// The error return is reserved for operational faults — a tree
		// failing the check is a successful call carrying passed=false.
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	cause := stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED
	if res.GetExecution() != nil && !golang.SuiteHealthy(res.GetExecution()) {
		cause = stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE
	}
	if len(res.GetWitnessDiagnostics()) > 0 {
		cause = stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE
	}
	prog.Terminal(cause)
	view, err := views.CheckView(res, in.View, ids)
	if err != nil {
		return nil, nil, err
	}
	var redRows []string
	blocked := 0
	for _, r := range res.GetCoverage().GetRequirements() {
		if coverage.RedBucket(r.GetBucket()) {
			// Policy-blocked rows restate the result-level diagnostic;
			// fold them behind it so the text digest carries the cause
			// once and the real reds stay visible
			// (REQ-check-witness-selection).
			if res.GetWitnessSelectionProblem() != "" && r.GetWitnessSelectionBlocked() {
				blocked++
				continue
			}
			// Scope-boundary rows restate the result's partial flag, and
			// checkLine already carries their folded count — the digest
			// keeps only the reds a scoped pass actually judged.
			if res.GetScopePartial() && r.GetScopeBlocked() {
				continue
			}
			row := fmt.Sprintf("%s [%s]", r.GetId(), enumWord(r.GetBucket().String(), "BUCKET_"))
			if reasons := r.GetReasons(); len(reasons) > 0 {
				row += ": " + reasons[0]
			}
			redRows = append(redRows, row)
		}
	}
	line := checkLine(res)
	for _, n := range res.GetPolicyNotices() {
		line += "\n" + n
	}
	for _, n := range res.GetResolutionNotices() {
		if strings.HasPrefix(n, "resolution typed: ") {
			// The per-symbol lines are the CLI's; the digest keeps the
			// account and the degradations.
			continue
		}
		line += "\n" + n
	}
	if p := res.GetWitnessSelectionProblem(); p != "" {
		line += "\n" + p
		if blocked > 0 {
			line += fmt.Sprintf("\n%d requirements are red solely on that boundary (policy-blocked; rows on the full view)", blocked)
		}
	}
	return summarized(withStamps(digest(line, redRows), prog), view)
}

// checkLine is the one-line text beside the structured result: the
// verdict and the load-bearing counts, never a duplicate serialization
// (REQ-mcp-response-contract).
func checkLine(res *stipulatorv1.CheckResult) string {
	verdict := "pass"
	if !res.GetPassed() {
		verdict = "fail"
	}
	if n := len(res.GetCompileProblems()); n > 0 {
		return fmt.Sprintf("check: %s (corpus does not compile: %d problems)", verdict, n)
	}
	if res.GetPolicyProblem() != nil {
		return fmt.Sprintf("check: %s (test policy problem)", verdict)
	}
	class := "witness-evidence"
	if res.GetSuiteHealthJudged() {
		class = "health-judged"
	}
	if res.GetScopePartial() {
		class = "scoped-partial: " + strings.Join(res.GetScopeIds(), ",")
	}
	violations := 0
	scopeBlocked := map[string]bool{}
	for _, r := range res.GetCoverage().GetRequirements() {
		if r.GetScopeBlocked() {
			scopeBlocked[r.GetId()] = true
		}
	}
	folded := 0
	for _, v := range res.GetCoverage().GetViolations() {
		if res.GetScopePartial() && scopeBlocked[v] {
			folded++
			continue
		}
		violations++
	}
	line := fmt.Sprintf("check: %s (%s; %d served, %d executed, %d uncacheable; %d violations)",
		verdict, class, res.GetTestsServed(), res.GetTestsExecuted(), res.GetTestsUncacheable(),
		violations)
	if folded > 0 {
		line += fmt.Sprintf(" (%d scope-blocked rows not executed)", folded)
	}
	// Observed red fails the verdict on its own (REQ-check-verdict), so
	// the line names it: a fail with zero violations must not read as
	// unexplained. Counted per diagnostic row, the same rows the summary
	// heads.
	if red := len(res.GetWitnessDiagnostics()) + len(res.GetExecution().GetDiagnostics()); red > 0 {
		line += fmt.Sprintf("; %d red executions (witness_failure_headings)", red)
		if red > views.HeadingCap {
			line += fmt.Sprintf(" — the first %d listed, the rest counted omitted", views.HeadingCap)
		}
	}
	return line
}
