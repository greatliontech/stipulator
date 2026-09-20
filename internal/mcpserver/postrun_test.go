package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A problem the witness run itself finds — a registration no tests- or
// proves-role binding backs, with the records themselves clean — refuses
// the coverage verbs and rides verify's report as a counted problem
// (REQ-check-preparation).
func TestPostRunProblemsRefuseTheCoverageVerbsAndRideVerifysReport(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	sess, _ := harnessWith(t, nil, func(s *Server) {
		s.runTests = func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return &verify.TestRun{Registrations: []verify.Registration{{Package: "example.com/p", Test: "TestRogue", Requirement: "REQ-m-b"}}}, nil
		}
	})
	for _, tool := range []string{"gate", "partitions"} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(textOf(res), "verification problems") || !strings.Contains(textOf(res), "no tests- or proves-role binding") {
			t.Fatalf("%s answered %s, want the post-run problem refusing the coverage judgment", tool, textOf(res))
		}
	}
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "verify", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(textOf(res), "1 problems") {
		t.Fatalf("verify answered %s, want the post-run problem counted on the report, never a refusal", textOf(res))
	}
}
