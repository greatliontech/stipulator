package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
)

// compileOut carries the diagnostics and, when the corpus is clean, the
// IR's counts. The counts are a property of the IR, and an error-severity
// diagnostic leaves no IR — so they are pointers, absent (not zero) on
// error: absent means "not computed", a present 0 means "genuinely empty".
type compileOut struct {
	// Capped; DiagnosticsOmitted counts the remainder so the truncation
	// is never silent (REQ-mcp-response-contract).
	Diagnostics        []string
	DiagnosticsOmitted int
	Requirements       *int
	Terms              *int
	Edges              *int
}

// proto is the compile result's wire message.
func (c compileOut) proto() *stipulatorv1.CompileResult {
	m := &stipulatorv1.CompileResult{}
	m.SetDiagnostics(c.Diagnostics)
	if c.DiagnosticsOmitted > 0 {
		m.SetDiagnosticsOmitted(int32(c.DiagnosticsOmitted))
	}
	if c.Requirements != nil {
		m.SetRequirements(int32(*c.Requirements))
	}
	if c.Terms != nil {
		m.SetTerms(int32(*c.Terms))
	}
	if c.Edges != nil {
		m.SetEdges(int32(*c.Edges))
	}
	return m
}

// compileDiagnosticCap bounds the compile tool's diagnostic list.
const compileDiagnosticCap = 50

func (s *Server) toolCompile(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]any, error) {
	spec, diags, err := compile.Compile(s.fsys())
	if err != nil {
		return nil, nil, err
	}
	out := compileOut{}
	for _, d := range diags {
		if len(out.Diagnostics) == compileDiagnosticCap {
			out.DiagnosticsOmitted = len(diags) - compileDiagnosticCap
			break
		}
		out.Diagnostics = append(out.Diagnostics, d.String())
	}
	if spec != nil {
		reqs, terms, edges := len(spec.GetRequirements()), len(spec.GetTerms()), len(spec.GetEdges())
		out.Requirements, out.Terms, out.Edges = &reqs, &terms, &edges
	}
	return projected(textOnly(digest(compileLine(out), out.Diagnostics)), out.proto())
}

// compileLine is the verdict line beside the structured compile result;
// the caller composes it with the capped diagnostic rows.
func compileLine(out compileOut) string {
	if n := len(out.Diagnostics) + out.DiagnosticsOmitted; n > 0 {
		return fmt.Sprintf("compile: %d diagnostics", n)
	}
	if out.Requirements != nil {
		return fmt.Sprintf("compile: ok, %d requirements", *out.Requirements)
	}
	return "compile: ok"
}
