package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/views"
)

type gateIn struct {
	View   string `json:"view,omitempty"`
	Ids    string `json:"ids,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	Filter string `json:"filter,omitempty"`
	Path   string `json:"path,omitempty"`
}

func (s *Server) toolGate(ctx context.Context, req *mcp.CallToolRequest, in gateIn) (*mcp.CallToolResult, map[string]any, error) {
	ctx, prog := s.startProgress(ctx, req)
	// The caller's vocabulary and the coverage policy are judged before
	// the witness run: a typo or a duplicated coverage cell refuses
	// before any child process (REQ-check-preparation).
	scope, err := scopeFrom(in.Ids, in.Bucket, in.Filter, in.Path)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if err := scope.Validate(); err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if err := views.ValidateCoverageView(in.View); err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prepared, rep, _, err := s.verifyPass(ctx, false, in.Ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if err := verificationProblems(rep); err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
	prog.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	cov := coverage.Evaluate(spec, rep, store, true, pol)
	m, err := views.CoverageView(cov, views.FactsFrom(spec, rep), in.View, scope)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return summarized(withStamps(viewLine("gate", m), prog), m)
}
