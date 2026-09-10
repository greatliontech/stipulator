package mcpserver

import (
	"github.com/greatliontech/stipulator/internal/corpus"
	"slices"

	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/verify"
)

type bindIn struct {
	Requirement string      `json:"requirement,omitempty"`
	Symbol      string      `json:"symbol,omitempty"`
	Role        string      `json:"role,omitempty"`
	Backend     string      `json:"backend,omitempty"`
	File        string      `json:"file,omitempty"`
	Clause      string      `json:"clause,omitempty"`
	Claims      []bindClaim `json:"claims,omitempty"`
}

type bindClaim struct {
	Requirement string `json:"requirement"`
	Symbol      string `json:"symbol"`
	Role        string `json:"role"`
	Backend     string `json:"backend,omitempty"`
	File        string `json:"file,omitempty"`
	Clause      string `json:"clause,omitempty"`
}

type writeOut struct {
	Wrote   []string
	Deleted []string
	Removed int
	// Notes surface non-silent consequences, e.g. a gap's landing
	// condition retarget.
	Notes []string
	// Check marks a preview: nothing was written, the rows say what an
	// apply would do - without it a zero-row check and a zero-write
	// apply are indistinguishable on the wire.
	Check bool
}

// apply lands a batch of record updates under compare-and-swap
// (REQ-record-cas): every precondition checks against the live tree
// before the first write, so a target that moved since the operation
// read it refuses the whole batch and a concurrent agent's records are
// never silently dropped.
func (s *Server) apply(ups []author.Update) (writeOut, error) {
	// One apply at a time: the SDK runs tool calls concurrently, and an
	// unserialized check-then-write would let two batches both pass
	// their preconditions then clobber each other — the precise loss
	// REQ-record-cas exists to refuse. A process-local mutex is
	// transient in-memory state, exactly what the clause sanctions.
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	seen := map[string]bool{}
	for _, up := range ups {
		if seen[up.Path] {
			return writeOut{}, fmt.Errorf("batch names %s twice; refusing the ambiguous apply", up.Path)
		}
		seen[up.Path] = true
		if up.Prior == nil && !up.PriorAbsent {
			return writeOut{}, fmt.Errorf("%s carries no precondition; the computing operation failed to stamp what it read", up.Path)
		}
		current, err := fs.ReadFile(s.fsys(), up.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if !up.PriorAbsent && up.Prior != nil {
				return writeOut{}, fmt.Errorf("%s vanished since the operation read it; re-run against the current tree", up.Path)
			}
		case err != nil:
			return writeOut{}, err
		case up.PriorAbsent:
			return writeOut{}, fmt.Errorf("%s appeared since the operation ran; re-run against the current tree", up.Path)
		case !bytes.Equal(current, up.Prior):
			return writeOut{}, fmt.Errorf("%s changed since the operation read it (a concurrent write?); re-run against the current tree", up.Path)
		}
	}
	// Admissibility is judged for the whole batch before any write: a
	// document the seam would refuse refuses the batch with nothing
	// written, never after the store half has landed
	// (REQ-change-retarget's all-or-nothing).
	for _, up := range ups {
		if err := admitWrite(s.fsys(), up.Path, up.Content != nil && up.Document); err != nil {
			return writeOut{}, err
		}
	}
	out := writeOut{}
	for _, up := range ups {
		if up.Content == nil {
			if err := s.remove(up.Path); err != nil {
				return writeOut{}, err
			}
			out.Deleted = append(out.Deleted, up.Path)
			continue
		}
		if err := s.write(up.Path, up.Content, up.Document); err != nil {
			return writeOut{}, err
		}
		out.Wrote = append(out.Wrote, up.Path)
	}
	return out, nil
}

// admitWrite is the confinement judgment (REQ-mcp-writes-confined): a
// clean local path under .stipulator/, or — for an update marked as a
// document rewrite — a document the corpus's manifest names, so a
// retarget's pointer rewrite lands in the spec document that names the
// pointer and nowhere else.
func admitWrite(fsys fs.FS, path string, document bool) error {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return fmt.Errorf("path %q escapes the corpus root", path)
	}
	// The prefix is judged on the clean spelling only: an embedded ".."
	// would satisfy a lexical prefix check while writing outside the
	// home.
	if path != pathpkg.Clean(path) {
		return fmt.Errorf("path %q is not a clean path", path)
	}
	if strings.HasPrefix(path, ".stipulator/") {
		return nil
	}
	if !document {
		return fmt.Errorf("path %q is outside .stipulator/ (the server writes nowhere else)", path)
	}
	m, err := corpus.LoadManifest(fsys)
	if err != nil {
		return fmt.Errorf("document rewrite of %q: %w", path, err)
	}
	docs, err := corpus.Enumerate(fsys, m)
	if err != nil {
		return fmt.Errorf("document rewrite of %q: %w", path, err)
	}
	if !slices.Contains(docs, path) {
		return fmt.Errorf("path %q is not a corpus document (the server rewrites enforcement pointers in corpus documents and nothing else)", path)
	}
	return nil
}

// proto is the write result's wire message: zero counts and an unset
// preview stay absent, as the projection's omitted fields.
func (w writeOut) proto() *stipulatorv1.WriteResult {
	m := &stipulatorv1.WriteResult{}
	m.SetWrote(w.Wrote)
	m.SetDeleted(w.Deleted)
	if w.Removed > 0 {
		m.SetRemoved(int32(w.Removed))
	}
	m.SetNotes(w.Notes)
	if w.Check {
		m.SetCheck(true)
	}
	return m
}

// result is the one-line Content beside the structured writeOut
// (REQ-mcp-response-contract's single payload encoding).
func (w writeOut) result() *mcp.CallToolResult {
	line := fmt.Sprintf("wrote %d, deleted %d", len(w.Wrote), len(w.Deleted))
	if w.Removed > 0 {
		line += fmt.Sprintf(", removed %d claims", w.Removed)
	}
	if len(w.Notes) > 0 {
		line += fmt.Sprintf("; %d notes", len(w.Notes))
	}
	return textOnly(line)
}

func (s *Server) toolBind(ctx context.Context, req *mcp.CallToolRequest, in bindIn) (*mcp.CallToolResult, map[string]any, error) {
	defaultBackend := in.Backend
	if defaultBackend == "" {
		defaultBackend = "go"
	}
	var reqs []author.BindRequest
	switch {
	case len(in.Claims) > 0:
		if in.Requirement != "" || in.Symbol != "" || in.Role != "" || in.File != "" || in.Clause != "" {
			return nil, nil, fmt.Errorf("give either claims or the single-claim fields, not both")
		}
		for _, c := range in.Claims {
			role, err := author.ParseRole(c.Role)
			if err != nil {
				return nil, nil, err
			}
			backendName := c.Backend
			if backendName == "" {
				backendName = defaultBackend
			}
			reqs = append(reqs, author.BindRequest{
				Requirement: c.Requirement, Symbol: c.Symbol, Backend: backendName,
				Role: role, File: c.File, Clause: c.Clause,
			})
		}
	default:
		role, err := author.ParseRole(in.Role)
		if err != nil {
			return nil, nil, err
		}
		reqs = append(reqs, author.BindRequest{
			Requirement: in.Requirement, Symbol: in.Symbol, Backend: defaultBackend,
			Role: role, File: in.File, Clause: in.Clause,
		})
	}
	ctx, prog := s.startProgress(ctx, req)
	prog.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	backends, err := s.backends(ctx, nil)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	defer verify.CloseBackends(backends)
	ups, err := author.Binds(s.fsys(), backends, reqs)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return projected(stampedResult(out.result(), prog), out.proto())
}

type unbindIn struct {
	Requirement string `json:"requirement"`
	Symbol      string `json:"symbol,omitempty"`
	Role        string `json:"role,omitempty"`
	Clause      string `json:"clause,omitempty"`
}

func (s *Server) toolUnbind(ctx context.Context, req *mcp.CallToolRequest, in unbindIn) (*mcp.CallToolResult, map[string]any, error) {
	role, err := author.ParseRole(in.Role)
	if err != nil {
		return nil, nil, err
	}
	ups, removed, err := author.Unbind(s.fsys(), in.Requirement, in.Symbol, role, in.Clause)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, err
	}
	out.Removed = removed
	return projected(out.result(), out.proto())
}

// partialPinError names what an interrupted ids-form pin already wrote:
// a later id's refusal must never read as "nothing written" over files
// the earlier ids moved.
func partialPinError(out writeOut, err error) error {
	if len(out.Wrote) == 0 {
		return err
	}
	return fmt.Errorf("%w (already re-pinned before the refusal: %s)", err, strings.Join(out.Wrote, ", "))
}
