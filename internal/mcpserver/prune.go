package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/prune"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

type pruneIn struct {
	Check    bool `json:"check,omitempty"`
	Dangling bool `json:"dangling,omitempty"`
	Store    bool `json:"store,omitempty"`
}

// toolPrune deletes resolved gap records. Detecting resolution is the
// same verify+coverage the gate performs — a resolved gap is one whose
// requirement is covered with any manual landing condition explicitly
// fired — and pruning is refused on a shaky reading: a verification
// problem could misreport a bucket and prune a still-load-bearing gap.
// It writes only under .stipulator/gaps/.
func (s *Server) toolPrune(ctx context.Context, req *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, map[string]any, error) {
	mode := prune.Mode{Check: in.Check, Dangling: in.Dangling, Store: in.Store}
	if err := mode.Validate(); err != nil {
		return nil, nil, err
	}
	deps := prune.Deps{
		Deps:    s.deps(),
		Root:    s.root,
		Compile: s.compileFresh,
		Load:    func() (*records.Store, error) { return records.Load(s.fsys()) },
	}
	// The store lives in the user cache, outside the corpus - the
	// .stipulator/ write confinement governs record writes, not the
	// tool's own cache.
	if mode.Store {
		res, err := prune.StoreGC(ctx, deps)
		if err != nil {
			return nil, nil, err
		}
		// The line rides Notes too: a structured-preferring client must
		// not read an empty object where the text names the outcome.
		out := writeOut{Notes: []string{fmt.Sprintf("store gc: %d record variant(s) removed, %d kept", res.Removed, res.Kept)}}
		if res.Resolutions != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("store gc: %d resolution record(s) removed, %d kept", res.Resolutions.Removed, res.Resolutions.Kept))
		}
		return projected(textOnly(strings.Join(out.Notes, "\n")), out.proto())
	}
	if mode.Dangling {
		prunes, err := prune.Dangling(deps)
		if err != nil {
			return nil, nil, err
		}
		if in.Check {
			out := writeOut{Check: true}
			for _, up := range prunes {
				out.Notes = append(out.Notes, "dangling gap lingers: "+up.Path)
			}
			if len(prunes) == 0 {
				out.Notes = append(out.Notes, "no dangling gap records")
			}
			return projected(out.result(), out.proto())
		}
		out, err := s.apply(prunes)
		if err != nil {
			return nil, nil, err
		}
		if len(prunes) == 0 {
			out.Notes = append(out.Notes, "no dangling gap records")
		}
		return projected(out.result(), out.proto())
	}
	ctx, prog := s.startProgress(ctx, req)
	res, err := prune.Evaluate(ctx, deps, false)
	if err != nil {
		var pe *prune.ProblemsError
		if errors.As(err, &pe) {
			err = verificationProblems(&verify.Report{Problems: pe.Problems})
		}
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if !res.Evaluated {
		out := writeOut{Notes: []string{"no gap records - nothing to evaluate"}, Check: in.Check}
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return projected(stampedResult(out.result(), prog), out.proto())
	}
	evaluated := res.Line()
	if in.Check {
		out := writeOut{Notes: []string{evaluated}, Check: true}
		for _, up := range res.Prunes {
			out.Notes = append(out.Notes, "resolved gap lingers: "+up.Path)
		}
		if len(res.Prunes) == 0 {
			out.Notes = append(out.Notes, "no resolved gap records linger")
		}
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return projected(stampedResult(out.result(), prog), out.proto())
	}
	out, err := s.apply(res.Prunes)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	out.Notes = append(out.Notes, evaluated)
	if len(res.Prunes) == 0 {
		out.Notes = append(out.Notes, "no resolved gap records linger")
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return projected(stampedResult(out.result(), prog), out.proto())
}
