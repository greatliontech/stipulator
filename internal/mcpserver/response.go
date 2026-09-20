package mcpserver

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/wire"
)

// The response composition the served verbs share.

// withStamps appends the operation's phase-timing line to the text
// content — the notification-blind client's after-the-fact record that
// slow work was work, not a hang (REQ-mcp-progress's completed-call
// fallback). One bounded line; empty reporters append nothing.
func withStamps(text string, prog *progress.Reporter) string {
	if stamps := prog.Stamps(); stamps != "" {
		return text + "\n" + stamps
	}
	return text
}

// stampedResult is withStamps for write-shaped results: the timing line
// rides the TEXT content only — never writeOut's structured Notes, which
// enumerate operation consequences (REQ-mcp-progress's text-digest-only
// carve-out).
func stampedResult(res *mcp.CallToolResult, prog *progress.Reporter) *mcp.CallToolResult {
	if stamps := prog.Stamps(); stamps != "" && len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			tc.Text += "\n" + stamps
		}
	}
	return res
}

// projected pairs a tool's text result with its structured content: the
// one ProtoJSON projection of the result message (REQ-mcp-tools).
func projected(res *mcp.CallToolResult, m proto.Message) (*mcp.CallToolResult, map[string]any, error) {
	out, err := wire.StructuredContent(m)
	if err != nil {
		return nil, nil, err
	}
	return res, out, nil
}

// summarized emits one wire encoding of the payload: the structured
// result beside a one-line text summary. Leaving Content nil would make
// the SDK serialize the whole payload a second time as text
// (REQ-mcp-response-contract).
func summarized(line string, m proto.Message) (*mcp.CallToolResult, map[string]any, error) {
	return projected(textOnly(line), m)
}

// textOnly is the one-line Content beside a structured result — set so
// the SDK never serializes the whole payload a second time as text
// (REQ-mcp-response-contract).
func textOnly(line string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: line}}}
}

// digestRowCap bounds the action rows a text digest carries beside the
// structured payload (REQ-mcp-response-contract's bounded text digest).
const digestRowCap = 10

// digest composes the verdict line with capped action rows: a lossy
// projection for clients that expose text content only, never a second
// encoding of the payload. Truncation is counted, not silent.
func digest(line string, rows []string) string {
	omitted := 0
	if len(rows) > digestRowCap {
		omitted = len(rows) - digestRowCap
		rows = rows[:digestRowCap]
	}
	var b strings.Builder
	b.WriteString(line)
	for _, row := range rows {
		b.WriteString("\n")
		b.WriteString(row)
	}
	if omitted > 0 {
		b.WriteString(fmt.Sprintf("\n… and %d more", omitted))
	}
	return b.String()
}

// enumWord renders a proto enum constant under its type prefix as
// lower-case words: enumWord("RESOLUTION_NOT_FOUND", "RESOLUTION_") ->
// "not found". Taking the last segment instead would invert multi-word
// values ("not found" -> "found") - a red row reading healthy.
func enumWord(name, prefix string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(name, prefix), "_", " "))
}

// viewLine names one view result for the text content: the verdict line
// plus capped action rows, so a text-only client can identify what to
// repair without the structured payload (REQ-mcp-response-contract).
func viewLine(op string, m proto.Message) string {
	switch v := m.(type) {
	case *stipulatorv1.VerifySummary:
		return digest(fmt.Sprintf("verify: %d problems, %d stale, %d broken", v.GetProblems(), v.GetStale(), v.GetBroken()),
			v.GetWitnessFailureHeadings())
	case *stipulatorv1.VerifyReport:
		var rows []string
		for _, p := range v.GetProblems() {
			rows = append(rows, p.GetPath()+": "+p.GetMessage())
		}
		if len(rows) == 0 {
			for _, r := range v.GetResults() {
				rows = append(rows, fmt.Sprintf("%s ← %s [%s, %s]", r.GetRequirementId(), r.GetSymbol(), enumWord(r.GetResolution().String(), "RESOLUTION_"), enumWord(r.GetTestOutcome().String(), "TEST_OUTCOME_")))
			}
		}
		return digest(fmt.Sprintf("verify: %d problems, %d bindings", len(v.GetProblems()), len(v.GetResults())), rows)
	case *stipulatorv1.CoverageSummary:
		word := "pass"
		if !v.GetGatePasses() {
			word = "fail"
		}
		return digest(fmt.Sprintf("gate: %s, %d violations", word, len(v.GetViolations())), v.GetViolations())
	case *stipulatorv1.CoverageReport:
		word := "pass"
		if !v.GetGatePasses() {
			word = "fail"
		}
		var rows []string
		for _, r := range v.GetRequirements() {
			row := fmt.Sprintf("%s [%s]", r.GetId(), coverage.BucketWord(r.GetBucket()))
			if reasons := r.GetReasons(); len(reasons) > 0 {
				row += ": " + reasons[0]
			}
			rows = append(rows, row)
		}
		return digest(fmt.Sprintf("gate: %s, %d requirements, %d violations", word, len(v.GetRequirements()), len(v.GetViolations())), rows)
	}
	return op + " (structured content carries the payload)"
}
