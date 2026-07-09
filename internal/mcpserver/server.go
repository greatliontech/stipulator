// Package mcpserver exposes stipulator over the Model Context Protocol:
// the compiled corpus as resources, the operations as tools.
//
// Every read serves fresh state — the corpus is recompiled and records
// reloaded per request — and all writes are confined to the record stores
// under .stipulator/: the server never edits spec documents or source
// code. Tool results carry the report messages as JSON.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/bundle"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/dossier"
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/gitfs"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
)

// Server serves one repository. The function fields exist so tests can
// inject trees, backends, and test runs; New wires production behavior.
type Server struct {
	srv      *mcp.Server
	indexed  map[string]bool
	fsys     func() fs.FS
	backends func() (map[string]verify.Backend, error)
	runTests func() (*verify.TestRun, error)
	// stagedScope classifies the working-tree delta vs HEAD; it needs git
	// and a loaded backend, so it is a dir-bound closure.
	stagedScope func(spec *stipulatorv1.Spec, store *records.Store) (*harden.StagedReport, error)
	// coverageReminder lists covered bodies with no fresh kill-sheet; it
	// needs the backend and toolchain, so it too is dir-bound.
	coverageReminder func(spec *stipulatorv1.Spec, store *records.Store, covered []string, findings []harden.EngineFinding) (*harden.Reminder, error)
	write            func(path string, content []byte) error
	remove           func(path string) error
}

// New returns a server rooted at dir.
func New(dir string) *Server {
	return &Server{
		fsys:     func() fs.FS { return os.DirFS(dir) },
		backends: func() (map[string]verify.Backend, error) { return makeBackends(dir) },
		runTests: func() (*verify.TestRun, error) { return golang.RunTests(dir) },
		stagedScope: func(spec *stipulatorv1.Spec, store *records.Store) (*harden.StagedReport, error) {
			gb, err := golang.New(dir)
			if err != nil {
				return nil, err
			}
			changed, err := gitfs.Changed(dir)
			if err != nil {
				return nil, err
			}
			headFS, err := gitfs.FS(dir, "HEAD")
			if err != nil {
				return nil, err
			}
			head := func(p string) ([]byte, bool) {
				b, err := fs.ReadFile(headFS, p)
				return b, err == nil
			}
			return harden.StagedScope(spec, store, gb, changed, head), nil
		},
		coverageReminder: func(spec *stipulatorv1.Spec, store *records.Store, covered []string, findings []harden.EngineFinding) (*harden.Reminder, error) {
			gb, err := golang.New(dir)
			if err != nil {
				return nil, err
			}
			toolchain, err := golang.Toolchain(dir)
			if err != nil {
				return nil, err
			}
			return harden.CoverageReminder(spec, store, gb, toolchain, covered, findings)
		},
		write: func(path string, content []byte) error {
			// The server is corpus-bound: a caller-supplied path (the
			// targets tool's out) must not escape the tree.
			if !filepath.IsLocal(filepath.FromSlash(path)) {
				return fmt.Errorf("path %q escapes the corpus root", path)
			}
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			return os.WriteFile(full, content, 0o644)
		},
		remove: func(path string) error {
			return os.Remove(filepath.Join(dir, filepath.FromSlash(path)))
		},
	}
}

func makeBackends(dir string) (map[string]verify.Backend, error) {
	gb, err := golang.New(dir)
	if err != nil {
		return nil, err
	}
	return map[string]verify.Backend{"go": gb}, nil
}

// Run serves MCP over stdio until the context ends.
func (s *Server) Run(ctx context.Context) error {
	return s.MCP().Run(ctx, &mcp.StdioTransport{})
}

// MCP builds the protocol server: tools, resource templates, and the
// requirement index.
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stipulator", Version: "v0"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "targets",
		Description: "Export the mutation targets: every go implements-binding with its witness union and requirement ids, as stipulator's versioned targets document. A mutation engine (gomutant) consumes it and writes a findings document stipulator reads back by label. Scope with reqs/symbols.",
	}, s.toolTargets)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "compile",
		Description: "Compile the spec corpus; returns diagnostics (empty means clean) and counts.",
	}, s.toolCompile)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "verify",
		Description: "Check records against corpus and code. Default view is the summary (hygiene and witness counts, change signatures); view=bindings for per-binding rows, scoped with ids/filter/path. Set no_test to skip witnessing.",
	}, s.toolVerify)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gate",
		Description: "Coverage gate. Default view is the summary (gate_passes, counts, violations); view=reds or full for per-requirement rows; scope with ids/bucket/filter/path. Runs the test suite. Also folds in hardeningReminder: covered bodies with no fresh kill-sheet (advisory, never affects the verdict).",
	}, s.toolGate)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "bind",
		Description: "Author a validated binding claim: the requirement must exist, the symbol must resolve (generated files rejected), pins applied immediately. Errors explain what to fix.",
	}, s.toolBind)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "unbind",
		Description: "Remove binding claims for a requirement, optionally narrowed by symbol and role. Matching nothing is an error.",
	}, s.toolUnbind)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gap",
		Description: "Declare a coverage gap: requirement, reason, and exactly one landing condition (covered/exists/manual).",
	}, s.toolGap)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "attest_requirement",
		Description: "Author the weakest evidence: a reason-carrying voucher for a requirement, content-pinned; renders the distinct attested bucket only where the policy admits it, never covered.",
	}, s.toolAttestRequirement)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "pin",
		Description: "Backfill unset content pins and refresh shape pins; with ids, editorially re-pin those requirements' bindings to the current clause text (re-consent). Never silent: no-ops say so.",
	}, s.toolPin)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "dispose",
		Description: "Apply a spec-change disposition: kind editorial (re-pin after meaning-preserving edit), retire (tombstone a removed identity), or supersede (tombstone sources, retarget bindings to declaring successors).",
	}, s.toolDispose)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "prune",
		Description: "Delete resolved gap records — gaps whose requirement has reached the covered bucket, satisfied dead weight the gate advertises. Pass check=true to report what would be pruned without deleting. Writes only under .stipulator/gaps/.",
	}, s.toolPrune)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "context",
		Description: "Per-requirement dossier for ids (comma-separated): clause text with kind and keyword, coverage bucket with reasons, open gap, attestation, bindings with witness class and pin freshness, hardening roll-ups, and closure seeds. Pass slice=true for the code-slice declaration frontier. Facts only — selection is yours.",
	}, s.toolContext)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "partitions",
		Description: "Candidate work partitions for requirement ids (comma-separated; empty means all red requirements): closure-connected components with seeds, touched packages, and pairwise overlaps. Disjoint components can fan out in parallel.",
	}, s.toolPartitions)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_spec",
		Description: "Read the self-contained bundle for requirement ids (comma-separated): the requirements, their closure, terms, and context. Mirrors the bundle resource for clients without resource support.",
	}, s.toolReadSpec)

	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "stipulator://req/{id}",
		Name:        "requirement",
		Description: "A requirement's compiled view: source, canonical metadata, content hash.",
		MIMEType:    "text/markdown",
	}, s.readResource)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "stipulator://term/{name}",
		Name:        "term",
		Description: "A term definition.",
		MIMEType:    "text/markdown",
	}, s.readResource)
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "stipulator://bundle/{ids}",
		Name:        "bundle",
		Description: "Self-contained closure for comma-separated requirement ids.",
		MIMEType:    "text/markdown",
	}, s.readResource)

	// The requirement index: one listed resource per requirement, so
	// resources/list is spec browsing. Synced at startup and on every
	// successful operation, so the list is fresh as of the most recent
	// operation; reads themselves always recompile.
	s.srv = srv
	s.indexed = map[string]bool{}
	if spec, diags, err := compile.Compile(s.fsys()); err == nil && len(compile.Errors(diags)) == 0 {
		s.syncIndex(spec)
	}
	return srv
}

// syncIndex reconciles the listed requirement resources with the compiled
// corpus: additions listed, retirements removed.
func (s *Server) syncIndex(spec *stipulatorv1.Spec) {
	current := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		current[r.GetId()] = true
		if !s.indexed[r.GetId()] {
			s.srv.AddResource(&mcp.Resource{
				URI:         "stipulator://req/" + r.GetId(),
				Name:        r.GetId(),
				Description: truncate(r.GetText(), 96),
				MIMEType:    "text/markdown",
			}, s.readResource)
			s.indexed[r.GetId()] = true
		}
	}
	for id := range s.indexed {
		if !current[id] {
			s.srv.RemoveResources("stipulator://req/" + id)
			delete(s.indexed, id)
		}
	}
}

// policy loads the manifest's coverage-policy overrides; verification
// errors surface at compile time, so a load failure here is unreachable
// on a tree that compiled.
func (s *Server) policy() (*coverage.Policy, error) {
	m, err := corpus.LoadManifest(s.fsys())
	if err != nil {
		return nil, err
	}
	return coverage.PolicyFromManifest(m)
}

func (s *Server) compileFresh() (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(s.fsys())
	if err != nil {
		return nil, err
	}
	if errs := compile.Errors(diags); len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, d := range errs {
			msgs = append(msgs, d.String())
		}
		return nil, fmt.Errorf("corpus does not compile:\n%s", strings.Join(msgs, "\n"))
	}
	if s.srv != nil {
		s.syncIndex(spec)
	}
	return spec, nil
}

// --- tools ---

// compileOut carries the diagnostics and, when the corpus is clean, the
// IR's counts. The counts are a property of the IR, and an error-severity
// diagnostic leaves no IR — so they are pointers, absent (not zero) on
// error: absent means "not computed", a present 0 means "genuinely empty".
type compileOut struct {
	Diagnostics  []string `json:"diagnostics"`
	Requirements *int     `json:"requirements,omitempty"`
	Terms        *int     `json:"terms,omitempty"`
	Edges        *int     `json:"edges,omitempty"`
}

func (s *Server) toolCompile(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, compileOut, error) {
	spec, diags, err := compile.Compile(s.fsys())
	if err != nil {
		return nil, compileOut{}, err
	}
	out := compileOut{Diagnostics: []string{}}
	for _, d := range diags {
		out.Diagnostics = append(out.Diagnostics, d.String())
	}
	if spec != nil {
		reqs, terms, edges := len(spec.GetRequirements()), len(spec.GetTerms()), len(spec.GetEdges())
		out.Requirements, out.Terms, out.Edges = &reqs, &terms, &edges
	}
	return nil, out, nil
}

type verifyIn struct {
	NoTest bool   `json:"no_test,omitempty" jsonschema:"skip running tests (no witnesses)"`
	View   string `json:"view,omitempty" jsonschema:"summary (default: hygiene and witness counts with change signatures) or bindings (the per-binding rows)"`
	Ids    string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers to scope binding rows to"`
	Filter string `json:"filter,omitempty" jsonschema:"requirement-id glob over binding rows"`
	Path   string `json:"path,omitempty" jsonschema:"prefix over declaring document or symbol"`
}

func (s *Server) verifyPipeline(noTest bool) (*stipulatorv1.Spec, *verify.Report, *records.Store, error) {
	spec, err := s.compileFresh()
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, nil, nil, err
	}
	backends, err := s.backends()
	if err != nil {
		return nil, nil, nil, err
	}
	var tr *verify.TestRun
	if !noTest {
		tr, err = s.runTests()
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return spec, verify.Run(spec, store, backends, tr), store, nil
}

func (s *Server) toolVerify(ctx context.Context, req *mcp.CallToolRequest, in verifyIn) (*mcp.CallToolResult, map[string]any, error) {
	spec, rep, _, err := s.verifyPipeline(in.NoTest)
	if err != nil {
		return nil, nil, err
	}
	scope, err := scopeFrom(in.Ids, "", in.Filter, in.Path)
	if err != nil {
		return nil, nil, err
	}
	m, err := views.VerifyView(rep, views.FactsFrom(spec, rep), in.View, scope)
	if err != nil {
		return nil, nil, err
	}
	return protoJSON(m)
}

type gateIn struct {
	View   string `json:"view,omitempty" jsonschema:"summary (default: pass/fail + counts + violations), reds (red requirements with reasons), or full (every requirement)"`
	Ids    string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers to scope to"`
	Bucket string `json:"bucket,omitempty" jsonschema:"scope to one bucket: uncovered, stale, broken, covered, exempt, attested"`
	Filter string `json:"filter,omitempty" jsonschema:"requirement-id glob, e.g. REQ-arch-*"`
	Path   string `json:"path,omitempty" jsonschema:"prefix over declaring spec document or bound symbols, e.g. docs/specs/change.md or internal/corpus"`
}

func (s *Server) toolGate(ctx context.Context, req *mcp.CallToolRequest, in gateIn) (*mcp.CallToolResult, map[string]any, error) {
	spec, rep, store, err := s.verifyPipeline(false)
	if err != nil {
		return nil, nil, err
	}
	if len(rep.Problems) > 0 {
		msgs := make([]string, 0, len(rep.Problems))
		for _, p := range rep.Problems {
			msgs = append(msgs, p.String())
		}
		return nil, nil, fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n"))
	}
	pol, err := s.policy()
	if err != nil {
		return nil, nil, err
	}
	cov := coverage.Evaluate(spec, rep, store, true, pol)
	scope, err := scopeFrom(in.Ids, in.Bucket, in.Filter, in.Path)
	if err != nil {
		return nil, nil, err
	}
	m, err := views.CoverageView(cov, views.FactsFrom(spec, rep), in.View, scope)
	if err != nil {
		return nil, nil, err
	}
	res, out, err := protoJSON(m)
	if err != nil {
		return nil, nil, err
	}
	// Fold in the covered-but-unhardened reminder (advisory, never gates).
	var covered []string
	for _, r := range cov.Requirements {
		if r.Bucket == coverage.Covered {
			covered = append(covered, r.Id)
		}
	}
	findings, ferr := harden.LoadFindings(s.fsys(), harden.FindingsPath)
	var reminder *harden.Reminder
	rerr := ferr
	if rerr == nil {
		reminder, rerr = s.coverageReminder(spec, store, covered, findings)
	}
	foldReminder(out, reminder, rerr)
	return res, out, nil
}

// foldReminder attaches the hardening reminder to a gate result. It never
// fails the call: the reminder is advisory (REQ-harden-coverage-reminder), so
// a computation error degrades to an empty reminder plus a diagnostic, never
// clobbering the gate verdict already in out — mirroring the CLI, which warns
// and proceeds.
func foldReminder(out map[string]any, reminder *harden.Reminder, err error) {
	out["hardeningReminder"] = harden.ReminderMap(reminder)
	if err != nil {
		out["hardeningReminderError"] = err.Error()
	}
}

// scopeFrom builds a scope from tool params, tolerating the same id
// encodings splitIDs does.
func scopeFrom(ids, bucket, filter, pathPrefix string) (views.Scope, error) {
	sc := views.Scope{Bucket: bucket, Filter: filter, Path: pathPrefix}
	if strings.TrimSpace(ids) != "" {
		parsed, err := splitIDs(ids)
		if err != nil {
			return views.Scope{}, err
		}
		sc.Ids = parsed
	}
	return sc, nil
}

type bindIn struct {
	Requirement string `json:"requirement" jsonschema:"requirement identifier"`
	Symbol      string `json:"symbol" jsonschema:"backend-scoped symbol reference"`
	Role        string `json:"role" jsonschema:"implements, tests, or proves"`
	Backend     string `json:"backend,omitempty" jsonschema:"language backend (default go)"`
	File        string `json:"file,omitempty" jsonschema:"target binding file (derived when empty)"`
}

type writeOut struct {
	Wrote   []string `json:"wrote,omitempty"`
	Deleted []string `json:"deleted,omitempty"`
	Removed int      `json:"removed,omitempty"`
	// Notes surface non-silent consequences, e.g. a gap's landing
	// condition retarget.
	Notes []string `json:"notes,omitempty"`
}

func (s *Server) toolBind(ctx context.Context, req *mcp.CallToolRequest, in bindIn) (*mcp.CallToolResult, writeOut, error) {
	role, err := author.ParseRole(in.Role)
	if err != nil {
		return nil, writeOut{}, err
	}
	backendName := in.Backend
	if backendName == "" {
		backendName = "go"
	}
	backends, err := s.backends()
	if err != nil {
		return nil, writeOut{}, err
	}
	up, err := author.Bind(s.fsys(), backends, author.BindRequest{
		Requirement: in.Requirement, Symbol: in.Symbol, Backend: backendName,
		Role: role, File: in.File,
	})
	if err != nil {
		return nil, writeOut{}, err
	}
	if err := s.write(up.Path, up.Content); err != nil {
		return nil, writeOut{}, err
	}
	return nil, writeOut{Wrote: []string{up.Path}}, nil
}

type unbindIn struct {
	Requirement string `json:"requirement" jsonschema:"requirement identifier"`
	Symbol      string `json:"symbol,omitempty" jsonschema:"narrow to one symbol"`
	Role        string `json:"role,omitempty" jsonschema:"narrow to one role"`
}

func (s *Server) toolUnbind(ctx context.Context, req *mcp.CallToolRequest, in unbindIn) (*mcp.CallToolResult, writeOut, error) {
	role, err := author.ParseRole(in.Role)
	if err != nil {
		return nil, writeOut{}, err
	}
	ups, removed, err := author.Unbind(s.fsys(), in.Requirement, in.Symbol, role)
	if err != nil {
		return nil, writeOut{}, err
	}
	out := writeOut{Removed: removed}
	for _, up := range ups {
		if up.Content == nil {
			if err := s.remove(up.Path); err != nil {
				return nil, writeOut{}, err
			}
			out.Deleted = append(out.Deleted, up.Path)
			continue
		}
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		out.Wrote = append(out.Wrote, up.Path)
	}
	return nil, out, nil
}

type gapIn struct {
	Requirement string `json:"requirement" jsonschema:"requirement identifier"`
	Reason      string `json:"reason" jsonschema:"why the gap exists"`
	Covered     string `json:"covered,omitempty" jsonschema:"lands when this requirement is covered"`
	Exists      string `json:"exists,omitempty" jsonschema:"lands when this requirement exists"`
	Manual      string `json:"manual,omitempty" jsonschema:"lands on this externally judged condition, fired explicitly"`
}

func (s *Server) toolGap(ctx context.Context, req *mcp.CallToolRequest, in gapIn) (*mcp.CallToolResult, writeOut, error) {
	g := &stipulatorv1.Gap{}
	g.SetRequirementId(in.Requirement)
	g.SetReason(in.Reason)
	lc, err := author.NewLandingCondition(in.Covered, in.Exists, in.Manual)
	if err != nil {
		return nil, writeOut{}, err
	}
	if lc != nil {
		g.SetLands(lc)
	}
	up, prior, err := author.Gap(s.fsys(), g)
	if err != nil {
		return nil, writeOut{}, err
	}
	if prior != nil && !proto.Equal(prior.GetLands(), g.GetLands()) {
		// A retarget is never silent: the wire result names old and new.
		out := writeOut{Wrote: []string{up.Path}, Notes: []string{
			"landing retargeted: " + author.LandingConditionString(prior.GetLands()) + " -> " + author.LandingConditionString(g.GetLands()),
		}}
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		return nil, out, nil
	}
	if err := s.write(up.Path, up.Content); err != nil {
		return nil, writeOut{}, err
	}
	return nil, writeOut{Wrote: []string{up.Path}}, nil
}

type attestSurvivorIn struct {
	Symbol   string `json:"symbol" jsonschema:"mutated symbol whose sheet carries the survivor"`
	Position string `json:"position" jsonschema:"survivor position as printed by harden (file.go:line:col)"`
	Operator string `json:"operator" jsonschema:"survivor operator as printed by harden"`
	Reason   string `json:"reason" jsonschema:"why the mutant is equivalent or accepted"`
}

type attestRequirementIn struct {
	Requirement string `json:"requirement" jsonschema:"requirement identifier"`
	Reason      string `json:"reason,omitempty" jsonschema:"why the requirement is judged satisfied (required unless retracting)"`
	Retract     bool   `json:"retract,omitempty" jsonschema:"withdraw the requirement's judgment instead of authoring one"`
}

func (s *Server) toolAttestRequirement(ctx context.Context, req *mcp.CallToolRequest, in attestRequirementIn) (*mcp.CallToolResult, writeOut, error) {
	if in.Retract {
		up, prior, err := author.RetractAttestation(s.fsys(), in.Requirement)
		if err != nil {
			return nil, writeOut{}, err
		}
		out := writeOut{Notes: []string{"retracted judgment: " + prior.GetReason()}}
		if up.Content == nil {
			out.Deleted = []string{up.Path}
			if err := s.remove(up.Path); err != nil {
				return nil, writeOut{}, err
			}
			return nil, out, nil
		}
		out.Wrote = []string{up.Path}
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		return nil, out, nil
	}
	up, prior, err := author.AttestRequirement(s.fsys(), in.Requirement, in.Reason)
	if err != nil {
		return nil, writeOut{}, err
	}
	if err := s.write(up.Path, up.Content); err != nil {
		return nil, writeOut{}, err
	}
	out := writeOut{Wrote: []string{up.Path}}
	if prior != nil {
		out.Notes = []string{"replaced judgment: " + prior.GetReason()}
	}
	return nil, out, nil
}

type pinIn struct {
	Ids string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers to editorially re-pin; empty backfills unset pins"`
}

func (s *Server) toolPin(ctx context.Context, req *mcp.CallToolRequest, in pinIn) (*mcp.CallToolResult, writeOut, error) {
	if in.Ids != "" {
		ids, err := splitIDs(in.Ids)
		if err != nil {
			return nil, writeOut{}, err
		}
		out := writeOut{}
		for _, id := range ids {
			ups, err := author.Editorial(s.fsys(), id)
			if errors.Is(err, author.ErrNothingStale) {
				out.Notes = append(out.Notes, id+": pins current")
				continue
			}
			if err != nil {
				return nil, writeOut{}, err
			}
			for _, up := range ups {
				if err := s.write(up.Path, up.Content); err != nil {
					return nil, writeOut{}, err
				}
				out.Wrote = append(out.Wrote, up.Path)
			}
		}
		return nil, out, nil
	}
	spec, err := s.compileFresh()
	if err != nil {
		return nil, writeOut{}, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, writeOut{}, err
	}
	hashes := map[string]string{}
	for _, r := range spec.GetRequirements() {
		hashes[r.GetId()] = r.GetContentHash()
	}
	backends, err := s.backends()
	if err != nil {
		return nil, writeOut{}, err
	}
	shapes := map[string]string{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			be, ok := backends[b.GetBackend()]
			if !ok {
				continue
			}
			if res, shape, err := be.Resolve(b.GetSymbol()); err == nil && res == verify.Resolved {
				shapes[records.ShapeKey(b.GetBackend(), b.GetSymbol())] = shape
			}
		}
	}
	updates, err := records.Pin(store, hashes, shapes)
	if err != nil {
		return nil, writeOut{}, err
	}
	out := writeOut{}
	for p, c := range updates {
		if err := s.write(p, c); err != nil {
			return nil, writeOut{}, err
		}
		out.Wrote = append(out.Wrote, p)
	}
	if len(out.Wrote) == 0 {
		// A no-op must say so: a silent {} reads as "did something,
		// reported nothing".
		out.Notes = []string{"all pins current"}
	}
	slices.Sort(out.Wrote)
	return nil, out, nil
}

type readSpecIn struct {
	Ids string `json:"ids" jsonschema:"comma-separated requirement identifiers"`
}

type readSpecOut struct {
	Markdown string `json:"markdown"`
}

func (s *Server) toolReadSpec(ctx context.Context, req *mcp.CallToolRequest, in readSpecIn) (*mcp.CallToolResult, readSpecOut, error) {
	md, err := s.bundleMarkdown(in.Ids)
	if err != nil {
		return nil, readSpecOut{}, err
	}
	return nil, readSpecOut{Markdown: md}, nil
}

// stagedEntry is one classified surface on the wire.
type stagedEntry struct {
	Path         string   `json:"path"`
	Symbol       string   `json:"symbol,omitempty"`
	Class        string   `json:"class"`
	Requirements []string `json:"requirements,omitempty"`
}

// stagedOut mirrors the staged-delta classification (REQ-harden-staged-scope):
// every changed surface with its disposition, plus a coverable/manual/skipped
// roll-up. Advisory, never a gate.
type stagedOut struct {
	Entries   []stagedEntry `json:"entries"`
	Coverable int           `json:"coverable"`
	Manual    int           `json:"manual"`
	Skipped   int           `json:"skipped"`
}

type disposeIn struct {
	Kind        string `json:"kind" jsonschema:"editorial, retire, or supersede"`
	Requirement string `json:"requirement,omitempty" jsonschema:"target for editorial/retire"`
	From        string `json:"from,omitempty" jsonschema:"comma-separated sources for supersede"`
	Into        string `json:"into,omitempty" jsonschema:"comma-separated successors for supersede"`
	Force       bool   `json:"force,omitempty" jsonschema:"retire even when no record names the identity"`
}

func (s *Server) toolDispose(ctx context.Context, req *mcp.CallToolRequest, in disposeIn) (*mcp.CallToolResult, writeOut, error) {
	var ups []author.Update
	var err error
	switch in.Kind {
	case "editorial":
		ups, err = author.Editorial(s.fsys(), in.Requirement)
	case "retire":
		ups, err = author.Retire(s.fsys(), in.Requirement, in.Force)
	case "supersede":
		var from, into []string
		if from, err = splitIDs(in.From); err != nil {
			return nil, writeOut{}, fmt.Errorf("from: %w", err)
		}
		if into, err = splitIDs(in.Into); err != nil {
			return nil, writeOut{}, fmt.Errorf("into: %w", err)
		}
		ups, err = author.Supersede(s.fsys(), from, into, in.Force)
	default:
		return nil, writeOut{}, fmt.Errorf("unknown disposition kind %q (editorial, retire, supersede)", in.Kind)
	}
	if err != nil {
		return nil, writeOut{}, err
	}
	out := writeOut{}
	for _, up := range ups {
		if up.Content == nil {
			if err := s.remove(up.Path); err != nil {
				return nil, writeOut{}, err
			}
			out.Deleted = append(out.Deleted, up.Path)
			continue
		}
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		out.Wrote = append(out.Wrote, up.Path)
	}
	return nil, out, nil
}

type pruneIn struct {
	Check bool `json:"check,omitempty" jsonschema:"report which resolved gaps would be pruned, deleting nothing"`
}

// toolPrune deletes resolved gap records. Detecting resolution is the same
// verify+coverage the gate performs — a resolved gap is one whose
// requirement has reached the covered bucket — and pruning is refused on a
// shaky reading: a verification problem could misreport a bucket and prune a
// still-load-bearing gap. It writes only under .stipulator/gaps/.
func (s *Server) toolPrune(ctx context.Context, req *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, writeOut, error) {
	spec, rep, store, err := s.verifyPipeline(false)
	if err != nil {
		return nil, writeOut{}, err
	}
	if len(rep.Problems) > 0 {
		msgs := make([]string, 0, len(rep.Problems))
		for _, p := range rep.Problems {
			msgs = append(msgs, p.String())
		}
		return nil, writeOut{}, fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n"))
	}
	pol, err := s.policy()
	if err != nil {
		return nil, writeOut{}, err
	}
	cov := coverage.Evaluate(spec, rep, store, true, pol)
	resolved := map[string]bool{}
	for _, g := range cov.Gaps {
		if g.State == coverage.Resolved {
			resolved[g.RequirementId] = true
		}
	}
	prunes := author.PruneResolvedGaps(store, resolved)
	if in.Check {
		out := writeOut{}
		for _, up := range prunes {
			out.Notes = append(out.Notes, "resolved gap lingers: "+up.Path)
		}
		return nil, out, nil
	}
	out := writeOut{}
	for _, up := range prunes {
		if err := s.remove(up.Path); err != nil {
			return nil, writeOut{}, err
		}
		out.Deleted = append(out.Deleted, up.Path)
	}
	return nil, out, nil
}

type contextIn struct {
	Ids   string `json:"ids" jsonschema:"comma-separated requirement identifiers"`
	Slice bool   `json:"slice,omitempty" jsonschema:"include the code-slice declaration frontier (the expensive leg)"`
}

func (s *Server) toolContext(ctx context.Context, req *mcp.CallToolRequest, in contextIn) (*mcp.CallToolResult, map[string]any, error) {
	ids, err := splitIDs(in.Ids)
	if err != nil {
		return nil, nil, err
	}
	spec, vr, store, err := s.verifyPipeline(false)
	if err != nil {
		return nil, nil, err
	}
	pol, err := s.policy()
	if err != nil {
		return nil, nil, err
	}
	cr := coverage.Evaluate(spec, vr, store, true, pol)
	findings, err := harden.LoadFindings(s.fsys(), harden.FindingsPath)
	if err != nil {
		return nil, nil, err
	}
	dossiers, err := dossier.Build(spec, vr, cr, store, findings, ids)
	if err != nil {
		return nil, nil, err
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
		backends, err := s.backends()
		if err != nil {
			return nil, nil, err
		}
		_, decls, err := facts.Context(spec, store, backends, ids)
		if err != nil {
			return nil, nil, err
		}
		out.SetDeclarations(facts.ContextProto(nil, decls).GetDeclarations())
	}
	return protoJSON(out)
}

type partitionsIn struct {
	Ids string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers; empty means all red requirements"`
}

func (s *Server) toolPartitions(ctx context.Context, req *mcp.CallToolRequest, in partitionsIn) (*mcp.CallToolResult, map[string]any, error) {
	spec, rep, store, err := s.verifyPipeline(false)
	if err != nil {
		return nil, nil, err
	}
	if len(rep.Problems) > 0 {
		return nil, nil, fmt.Errorf("verification problems; fix records first")
	}
	backends, err := s.backends()
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	if strings.TrimSpace(in.Ids) != "" {
		ids, err = splitIDs(in.Ids)
		if err != nil {
			return nil, nil, err
		}
	} else {
		pol, perr := s.policy()
		if perr != nil {
			return nil, nil, perr
		}
		cov := coverage.Evaluate(spec, rep, store, true, pol)
		for _, r := range cov.Requirements {
			switch r.Bucket {
			case coverage.Uncovered, coverage.Stale, coverage.Broken:
				ids = append(ids, r.Id)
			}
		}
	}
	pr, err := facts.Partitions(spec, store, backends, ids)
	if err != nil {
		return nil, nil, err
	}
	return protoJSON(pr.Proto())
}

// splitIDsLoose splits a comma list; empty input is an empty selection,
// not an error.
func splitIDsLoose(commaIDs string) ([]string, error) {
	if strings.TrimSpace(commaIDs) == "" {
		return nil, nil
	}
	return splitIDs(commaIDs)
}

func splitIDs(commaIDs string) ([]string, error) {
	trimmed := strings.TrimSpace(commaIDs)
	// Tolerate a JSON-array-encoded list: clients that serialize the ids
	// field as an array deliver it as one string, and treating it as a
	// single identifier produces a mangled unknown-id error.
	if strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, fmt.Errorf("ids looks like a JSON array but does not parse: %w", err)
		}
		var ids []string
		for _, id := range arr {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("no requirement identifiers given")
		}
		return ids, nil
	}
	var ids []string
	for _, id := range strings.Split(commaIDs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no requirement identifiers given")
	}
	return ids, nil
}

// --- resources ---

func (s *Server) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	switch {
	case strings.HasPrefix(uri, "stipulator://req/"):
		id := strings.TrimPrefix(uri, "stipulator://req/")
		spec, err := s.compileFresh()
		if err != nil {
			return nil, err
		}
		for _, r := range spec.GetRequirements() {
			if r.GetId() == id {
				md := fmt.Sprintf("%s\n\n> id: %s | kind: %s | keyword: %s | content_hash: %s\n",
					r.GetSource(), r.GetId(),
					strings.ToLower(strings.TrimPrefix(r.GetKind().String(), "CLAUSE_KIND_")),
					strings.TrimPrefix(r.GetKeyword().String(), "KEYWORD_"),
					r.GetContentHash())
				return textResource(uri, "text/markdown", md), nil
			}
		}
		return nil, mcp.ResourceNotFoundError(uri)
	case strings.HasPrefix(uri, "stipulator://term/"):
		name := strings.TrimPrefix(uri, "stipulator://term/")
		spec, err := s.compileFresh()
		if err != nil {
			return nil, err
		}
		for _, t := range spec.GetTerms() {
			if strings.EqualFold(t.GetName(), name) {
				return textResource(uri, "text/markdown", t.GetSource()+"\n"), nil
			}
		}
		return nil, mcp.ResourceNotFoundError(uri)
	case strings.HasPrefix(uri, "stipulator://bundle/"):
		ids := strings.TrimPrefix(uri, "stipulator://bundle/")
		md, err := s.bundleMarkdown(ids)
		if err != nil {
			return nil, err
		}
		return textResource(uri, "text/markdown", md), nil
	}
	return nil, mcp.ResourceNotFoundError(uri)
}

func (s *Server) bundleMarkdown(commaIDs string) (string, error) {
	spec, err := s.compileFresh()
	if err != nil {
		return "", err
	}
	var ids []string
	for _, id := range strings.Split(commaIDs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no requirement identifiers given")
	}
	b, err := bundle.Compute(spec, ids)
	if err != nil {
		return "", err
	}
	return bundle.Markdown(b, ids), nil
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: mime, Text: text,
	}}}
}

// protoJSON renders a report message as the tool's structured output.
// stagedResult renders a staged-delta classification as JSON: every entry
// plus the coverable/manual/skipped roll-up.
func stagedResult(rep *harden.StagedReport) (*mcp.CallToolResult, map[string]any, error) {
	out := stagedOut{Entries: []stagedEntry{}}
	for _, e := range rep.Entries {
		out.Entries = append(out.Entries, stagedEntry{
			Path: e.Path, Symbol: e.Symbol, Class: string(e.Class), Requirements: e.Requirements,
		})
		switch e.Class {
		case harden.Covered:
			out.Coverable++
		case harden.GeneratedOrData:
			out.Skipped++
		default:
			out.Manual++
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil, err
	}
	return nil, m, nil
}

func protoJSON(m proto.Message) (*mcp.CallToolResult, map[string]any, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil, nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

type targetsIn struct {
	Reqs    string `json:"reqs,omitempty" jsonschema:"comma-separated requirement identifiers; empty means all bound"`
	Symbols string `json:"symbols,omitempty" jsonschema:"comma-separated implementation symbols filter"`
	Out     string `json:"out,omitempty" jsonschema:"tree-relative path to write the export to; empty returns it inline"`
	Staged  bool   `json:"staged_diff,omitempty" jsonschema:"classify the working-tree delta vs HEAD instead of exporting: which changed surfaces the mutation flow covers and which need manual mutation"`
}

func (s *Server) toolTargets(ctx context.Context, req *mcp.CallToolRequest, in targetsIn) (*mcp.CallToolResult, map[string]any, error) {
	spec, err := s.compileFresh()
	if err != nil {
		return nil, nil, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, nil, err
	}
	if in.Staged {
		rep, err := s.stagedScope(spec, store)
		if err != nil {
			return nil, nil, err
		}
		return stagedResult(rep)
	}
	reqs, err := splitIDsLoose(in.Reqs)
	if err != nil {
		return nil, nil, err
	}
	syms, err := splitIDsLoose(in.Symbols)
	if err != nil {
		return nil, nil, err
	}
	targets := harden.Plan(spec, store, reqs, syms)
	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("no targets: no go implements-bindings match the selection")
	}
	doc, err := harden.ExportTargets(targets)
	if err != nil {
		return nil, nil, err
	}
	if in.Out != "" {
		if err := s.write(in.Out, append(doc, '\n')); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"wrote": in.Out, "targets": len(targets)}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return nil, nil, err
	}
	return nil, m, nil
}
