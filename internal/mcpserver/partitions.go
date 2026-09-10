package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/wire"
)

type partitionsIn struct {
	Ids        string `json:"ids,omitempty"`
	NoTest     bool   `json:"no_test,omitempty"`
	ExportPath string `json:"export_path,omitempty"`
}

func (s *Server) toolPartitions(ctx context.Context, req *mcp.CallToolRequest, in partitionsIn) (*mcp.CallToolResult, map[string]any, error) {
	if err := validExportPath(in.ExportPath); err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	prepared, rep, _, err := s.verifyPass(ctx, in.NoTest, in.Ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if err := verificationProblems(rep); err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	spec, store := prepared.Spec, prepared.Store
	backends, err := s.backends(ctx, nil)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	defer verify.CloseBackends(backends)
	var ids []string
	if strings.TrimSpace(in.Ids) != "" {
		ids, err = splitIDs(in.Ids)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
	} else {
		pol, perr := s.policy()
		if perr != nil {
			return nil, nil, terminalToolError(prog, ctx, perr)
		}
		prog.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
		cov := coverage.Evaluate(spec, rep, store, !in.NoTest, pol)
		for _, r := range cov.Requirements {
			if r.Bucket.Red() {
				ids = append(ids, r.Id)
			}
		}
	}
	pr, err := facts.Partitions(spec, store, backends, ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	// The export carries the FULL pairwise overlap set — the explicit
	// request the capped wire default points at. Terminal after the
	// export write; the export line carries the phase stamps like every
	// completed suite-running result.
	if in.ExportPath != "" {
		doc, err := wire.CanonicalJSON(pr.ProtoUncapped())
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		res, structured, err := s.exportTo(in.ExportPath, doc, "partitions")
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return stampedResult(res, prog), structured, nil
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	m := pr.Proto()
	var componentRows []string
	for i, component := range m.GetComponents() {
		ids := component.GetRequirementIds()
		head := strings.Join(ids, ", ")
		if len(ids) > 3 {
			head = strings.Join(ids[:3], ", ") + fmt.Sprintf(" +%d", len(ids)-3)
		}
		componentRows = append(componentRows, fmt.Sprintf("component %d (%d pkgs): %s", i+1, len(component.GetPackages()), head))
	}
	line := fmt.Sprintf("partitions: %d components, %d overlaps (%d omitted)", len(m.GetComponents()), len(m.GetOverlaps()), m.GetOverlapsOmitted())
	if len(m.GetComponents()) == 0 && strings.TrimSpace(in.Ids) == "" {
		line += " - no red requirements, nothing to partition"
	}
	return summarized(withStamps(digest(line, componentRows), prog), m)
}
