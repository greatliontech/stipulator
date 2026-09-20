package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verifyrun"
)

type gapIn struct {
	Requirement  string `json:"requirement,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Covered      string `json:"covered,omitempty"`
	Exists       string `json:"exists,omitempty"`
	Manual       string `json:"manual,omitempty"`
	Fired        bool   `json:"fired,omitempty"`
	Contradicted bool   `json:"contradicted,omitempty"`
	Retract      bool   `json:"retract,omitempty"`
	Excuses      string `json:"excuses,omitempty"`
	List         bool   `json:"list,omitempty"`
}

// gapOut is the gap tool's result: the write fields, plus the list
// form's read rows.
type gapOut struct {
	writeOut
	// Gaps is the list form's read surface - the wire GapReport rows.
	Gaps []*stipulatorv1.GapReport
	// GapsOmitted counts rows beyond the response cap - the records on
	// disk carry the full set (REQ-mcp-response-contract's envelope).
	GapsOmitted int
}

// proto is the list's wire message.
func (g gapOut) proto() *stipulatorv1.GapListResult {
	w := g.writeOut.proto()
	m := &stipulatorv1.GapListResult{}
	m.SetWrote(w.GetWrote())
	m.SetDeleted(w.GetDeleted())
	if w.HasRemoved() {
		m.SetRemoved(w.GetRemoved())
	}
	m.SetNotes(w.GetNotes())
	if w.HasCheck() {
		m.SetCheck(true)
	}
	m.SetGaps(g.Gaps)
	if g.GapsOmitted > 0 {
		m.SetGapsOmitted(int32(g.GapsOmitted))
	}
	return m
}

func (s *Server) toolGap(ctx context.Context, req *mcp.CallToolRequest, in gapIn) (*mcp.CallToolResult, map[string]any, error) {
	conditioned := in.Covered != "" || in.Exists != "" || in.Manual != "" || in.Reason != "" || in.Excuses != "" || in.Contradicted
	if in.List {
		if in.Requirement != "" || conditioned || in.Fired || in.Retract {
			return nil, nil, fmt.Errorf("list is the read surface and combines with no write field: editing a gap is re-declaring it")
		}
		return s.gapList(ctx, req)
	}
	reqs, err := verbcore.SplitIDs(in.Requirement)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case in.Retract:
		if conditioned || in.Fired {
			return nil, nil, fmt.Errorf("retract takes only requirements: retraction deletes the record, conditions do not apply")
		}
		ups, err := author.RetractGaps(s.fsys(), reqs)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.apply(ups)
		if err != nil {
			return nil, nil, faulted(out, err)
		}
		return projected(out.result(), gapOut{writeOut: out}.proto())
	case in.Fired && in.Manual == "":
		if conditioned {
			return nil, nil, fmt.Errorf("fired alone fires existing gaps; declaring a new fired gap takes manual with fired")
		}
		ups, err := author.FireGaps(s.fsys(), reqs)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.apply(ups)
		if err != nil {
			return nil, nil, faulted(out, err)
		}
		return projected(out.result(), gapOut{writeOut: out}.proto())
	}
	lc, lcErr := author.NewLandingCondition(in.Covered, in.Exists, in.Manual, in.Fired, in.Contradicted)
	if lcErr != nil {
		return nil, nil, lcErr
	}
	var excuseNames []string
	for _, n := range strings.Split(in.Excuses, ",") {
		if n = strings.TrimSpace(n); n != "" {
			excuseNames = append(excuseNames, n)
		}
	}
	excuses, err := author.NewExcuses(excuseNames)
	if err != nil {
		return nil, nil, err
	}
	ups, notes, err := author.Gaps(s.fsys(), reqs, in.Reason, lc, excuses)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, faulted(out, err)
	}
	// A retarget is never silent: the wire result names old and new.
	out.Notes = notes
	return projected(out.result(), gapOut{writeOut: out}.proto())
}

// gapList is the gap tool's read surface: every record's declaration
// fields beside its evaluated lifecycle state, the evaluation scoped to
// the gap-relevant requirements exactly as prune's is
// (REQ-gap-resolved-pruned's narrowing), with dangling records listed
// rather than refused. It writes nothing.
func (s *Server) gapList(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, map[string]any, error) {
	ctx, prog := s.startProgress(ctx, req)
	prepared, rep, cov, err := verifyrun.Gaps(ctx, s.deps())
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	spec, store := prepared.Spec, prepared.Store
	if cov == nil {
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return projected(stampedResult(textOnly("no gap records"), prog), gapOut{writeOut: writeOut{Notes: []string{"no gap records"}}}.proto())
	}
	// The one row set both faces list — dangling rows first, so the
	// response cap keeps the rows demanding repair (REQ-gap-list).
	reports, dangling := verifyrun.GapRows(spec, store, cov)
	out := gapOut{Gaps: reports}
	const gapRowCap = 50
	if len(out.Gaps) > gapRowCap {
		out.GapsOmitted = len(out.Gaps) - gapRowCap
		out.Gaps = out.Gaps[:gapRowCap]
	}
	if n := len(rep.Problems); n > 0 {
		out.Notes = []string{verifyrun.MisreportCaveat(n, verifyrun.CaveatEvaluatedStates)}
	}
	// The one account of the listing, rows capped or not
	// (REQ-gap-list).
	line := coverage.GapListLine(reports, dangling)
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return projected(stampedResult(textOnly(line), prog), out.proto())
}
