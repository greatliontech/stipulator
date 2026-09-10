package mcpserver

import (
	"slices"

	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

type pinIn struct {
	Ids string `json:"ids,omitempty"`
}

func (s *Server) toolPin(ctx context.Context, req *mcp.CallToolRequest, in pinIn) (*mcp.CallToolResult, map[string]any, error) {
	if in.Ids != "" {
		ids, err := splitIDs(in.Ids)
		if err != nil {
			return nil, nil, err
		}
		out := writeOut{}
		// The editorial writes run first: an unknown id refuses before
		// any resolution cost is paid, and the shape judgment reads the
		// post-write store (shape pins are untouched by clause
		// re-consent, so the answers are order-independent).
		repinned := map[string]int{}
		// Every id is judged before the first write: a refusal (an id
		// outside the corpus, a clause claim the text no longer
		// resolves, a hand-commented record) refuses the whole batch
		// with nothing written, instead of surfacing after earlier ids
		// were applied and reporting "nothing written" over files that
		// moved. The writes still apply per id in order, each computed
		// over the store the previous id left — two ids sharing a
		// binding file must not race one compare-and-swap precondition.
		noOp := map[string]string{}
		for _, id := range ids {
			if _, _, err := author.Editorial(s.fsys(), id); err != nil && !errors.Is(err, author.ErrNothingStale) {
				return nil, nil, err
			}
		}
		for _, id := range ids {
			ups, consented, err := author.Editorial(s.fsys(), id)
			if errors.Is(err, author.ErrNothingStale) {
				noOp[id] = author.NoOpNote(err)
				continue
			}
			if err != nil {
				return nil, nil, partialPinError(out, err)
			}
			applied, err := s.apply(ups)
			if err != nil {
				return nil, nil, partialPinError(out, err)
			}
			out.Wrote = append(out.Wrote, applied.Wrote...)
			repinned[id] = len(ups)
			for _, line := range consented {
				out.Notes = append(out.Notes, id+": "+line)
			}
		}
		// The ids form re-consents clause text only; a shape mismatch
		// on the named requirement's bindings would survive it
		// untouched, so report it rather than let "pins current" read
		// as quiescence while the gate stays red. Resolution is
		// toolchain work — it reports phase progress like every long
		// pin arm (REQ-mcp-progress).
		ctx, prog := s.startProgress(ctx, req)
		prog.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
		store, err := records.Load(s.fsys())
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		backends, err := s.backends(ctx, nil)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		defer verify.CloseBackends(backends)
		wanted := map[string]bool{}
		for _, id := range ids {
			wanted[id] = true
		}
		// A resolution fault IS state that can empty the mismatch
		// answer: it rides Notes instead of silently reverting the
		// response to the quiescence claim (REQ-pin-backfill).
		mismatched := records.ShapeMismatched(store, ids, author.ResolveShapes(store, backends, wanted, func(symbol string, err error) {
			out.Notes = append(out.Notes, fmt.Sprintf("shape resolution skipped %s: %v - a shape mismatch there would go unreported this call", symbol, err))
		}))
		for _, id := range ids {
			syms := mismatched[id]
			switch {
			case repinned[id] > 0 && len(syms) > 0:
				out.Notes = append(out.Notes, id+": shape of "+strings.Join(syms, ", ")+" moved — ids re-consent clause text only, a blanket pin (no ids) re-pins shapes")
			case repinned[id] > 0:
			case len(syms) > 0:
				out.Notes = append(out.Notes, id+": "+noOp[id]+" — shape of "+strings.Join(syms, ", ")+" moved, and ids re-consent clause text only: a blanket pin (no ids) re-pins shapes")
			default:
				out.Notes = append(out.Notes, id+": "+noOp[id])
			}
		}
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return projected(stampedResult(out.result(), prog), out.proto())
	}
	spec, err := s.compileFresh()
	if err != nil {
		return nil, nil, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	prog.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	backends, err := s.backends(ctx, nil)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	defer verify.CloseBackends(backends)
	var resolutionNotes []string
	updates, preserved, reshaped, rehashed, err := records.Pin(store, records.HashesOf(spec), author.ResolveShapes(store, backends, nil, func(symbol string, err error) {
		resolutionNotes = append(resolutionNotes, fmt.Sprintf("shape resolution skipped %s: %v - its shape pin was not judged this call", symbol, err))
	}))
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	ups := make([]author.Update, 0, len(updates))
	for p, c := range updates {
		ups = append(ups, author.Update{Path: p, Content: c})
	}
	// The same store snapshot that fed Pin stamps the preconditions —
	// the backfill is a record write like any other (REQ-record-cas).
	author.StampPriors(store, ups)
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	// A no-op must say so: a silent {} reads as "did something, reported
	// nothing". Beside preserved differing pins the wording shifts —
	// "all pins current" is false exactly then.
	switch {
	case len(out.Wrote) == 0 && len(preserved) == 0:
		out.Notes = []string{"all pins current"}
	case len(out.Wrote) == 0:
		out.Notes = []string{"no pins backfilled"}
	}
	out.Notes = append(out.Notes, resolutionNotes...)
	if len(reshaped) > 0 {
		out.Notes = append(out.Notes, "shape pins refreshed (bound implementation moved): "+strings.Join(reshaped, ", "))
	}
	if len(rehashed) > 0 {
		out.Notes = append(out.Notes, "rehashed ("+records.RehashNote+"): "+strings.Join(rehashed, ", "))
	}
	if len(preserved) > 0 {
		out.Notes = append(out.Notes, "awaiting re-consent (pass ids): "+strings.Join(preserved, ", "))
	}
	slices.Sort(out.Wrote)
	return projected(out.result(), out.proto())
}
