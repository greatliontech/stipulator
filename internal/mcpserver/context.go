package mcpserver

import (
	"context"
	"fmt"
	pathpkg "path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/dossier"
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/wire"
)

type contextIn struct {
	Ids        string `json:"ids"`
	Slice      bool   `json:"slice,omitempty"`
	NoTest     bool   `json:"no_test,omitempty"`
	ExportPath string `json:"export_path,omitempty"`
}

func (s *Server) toolContext(ctx context.Context, req *mcp.CallToolRequest, in contextIn) (*mcp.CallToolResult, map[string]any, error) {
	if err := validExportPath(in.ExportPath); err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	ids, err := splitIDs(in.Ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prepared, vr, tr, err := s.verifyPass(ctx, in.NoTest, in.Ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	spec, store, pol := prepared.Spec, prepared.Store, prepared.Coverage
	prog.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	// Witnessed exactly when the pipeline ran a witness: the no-test
	// form and the record-only form a hygiene fault selects judge no
	// witness-backed requirement against absent evidence.
	cr := coverage.Evaluate(spec, vr, store, tr != nil, pol)
	dossiers, err := dossier.Build(spec, vr, cr, store, ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	out := &stipulatorv1.DossierReport{}
	out.SetDossiers(dossiers)
	// Orientation over a store that fails verification must say so, or
	// first-wins picks render without the problem that explains them.
	var problems []*stipulatorv1.Problem
	for _, p := range vr.Problems {
		m := &stipulatorv1.Problem{}
		m.SetPath(p.Path)
		m.SetMessage(p.Message)
		problems = append(problems, m)
	}
	out.SetProblems(problems)
	if in.Slice {
		// The declaration frontier is the expensive leg: it loads and
		// walks the bound packages' sources.
		prog.Phase(stipulatorv1.Phase_PHASE_CONTEXT_SLICE)
		backends, err := s.backends(ctx, nil)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		defer verify.CloseBackends(backends)
		_, decls, floor, err := facts.Context(spec, store, backends, ids)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		cp := facts.ContextProto(nil, decls, floor)
		out.SetDeclarations(cp.GetDeclarations())
		out.SetFloor(cp.GetFloor())
	}
	if in.ExportPath != "" {
		doc, err := wire.CanonicalJSON(out)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		res, structured, err := s.exportTo(in.ExportPath, doc, "context")
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		// Terminal after the export write - a failed write must not
		// follow a COMPLETED event - and the export line carries the
		// phase stamps like every completed suite-running result.
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return stampedResult(res, prog), structured, nil
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	var dossierRows []string
	for _, d := range out.GetDossiers() {
		row := d.GetRequirement().GetId()
		if cov := d.GetCoverage(); cov != nil {
			row += " [" + enumWord(cov.GetBucket().String(), "BUCKET_") + "]"
			if reasons := cov.GetReasons(); len(reasons) > 0 {
				row += ": " + reasons[0]
			}
		}
		dossierRows = append(dossierRows, row)
	}
	line := fmt.Sprintf("context: %d dossiers", len(out.GetDossiers()))
	if n := len(out.GetProblems()); n > 0 {
		line += fmt.Sprintf("; %d verification problems - dossier states may misreport, run verify", n)
	}
	return summarized(withStamps(digest(line, dossierRows), prog), out)
}

// exportTo validates a caller-named export path and writes the document
// under .stipulator/exports/ — the record-store home bounds every
// server write (REQ-mcp-writes-confined) — returning the location-only
// wire result.
func (s *Server) exportTo(exportPath string, doc []byte, what string) (*mcp.CallToolResult, map[string]any, error) {
	if err := validExportPath(exportPath); err != nil {
		return nil, nil, err
	}
	if err := s.write(exportPath, doc, false); err != nil {
		return nil, nil, err
	}
	m := &stipulatorv1.ExportResult{}
	m.SetExported(exportPath)
	m.SetBytes(int32(len(doc)))
	return projected(textOnly(fmt.Sprintf("%s: exported %d bytes to %s", what, len(doc), exportPath)), m)
}

// validExportPath refuses anything outside the export home. Tools with
// an expensive pass validate BEFORE running it: a typo must not cost a
// witness run only to be refused at write time.
func validExportPath(exportPath string) error {
	if exportPath == "" {
		return nil
	}
	clean := pathpkg.Clean(exportPath)
	if clean != exportPath || !strings.HasPrefix(clean, ".stipulator/exports/") || strings.Contains(clean, "..") {
		return fmt.Errorf("export_path must be a clean path under .stipulator/exports/ (the server writes nowhere else)")
	}
	return nil
}
