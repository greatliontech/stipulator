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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/harden"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Server serves one repository. The function fields exist so tests can
// inject trees, backends, and test runs; New wires production behavior.
type Server struct {
	srv      *mcp.Server
	indexed  map[string]bool
	fsys     func() fs.FS
	backends func() (map[string]verify.Backend, error)
	runTests func() (*verify.TestRun, error)
	harden   func(ctx context.Context, spec *stipulatorv1.Spec, store *records.Store, in hardenIn) (*harden.Report, error)
	write    func(path string, content []byte) error
	remove   func(path string) error
}

// New returns a server rooted at dir.
func New(dir string) *Server {
	return &Server{
		fsys:     func() fs.FS { return os.DirFS(dir) },
		backends: func() (map[string]verify.Backend, error) { return makeBackends(dir) },
		runTests: func() (*verify.TestRun, error) { return golang.RunTests(dir) },
		harden: func(ctx context.Context, spec *stipulatorv1.Spec, store *records.Store, in hardenIn) (*harden.Report, error) {
			gb, err := golang.New(dir)
			if err != nil {
				return nil, err
			}
			reqs, _ := splitIDsLoose(in.Reqs)
			syms, _ := splitIDsLoose(in.Symbols)
			targets := harden.Plan(spec, store, reqs, syms)
			if len(targets) == 0 {
				return nil, fmt.Errorf("no targets: no go implements-bindings match the selection")
			}
			budget := in.Budget
			if budget == 0 {
				budget = 24
			}
			return harden.Run(ctx, dir, gb, store, targets, harden.Options{Budget: budget, Force: in.Force, Jobs: in.Jobs})
		},
		write: func(path string, content []byte) error {
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
		Name:        "compile",
		Description: "Compile the spec corpus; returns diagnostics (empty means clean) and counts.",
	}, s.toolCompile)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "verify",
		Description: "Check binding and gap records against the corpus and code; returns the verify report. Set no_test to skip witnessing.",
	}, s.toolVerify)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gate",
		Description: "Full conformance verdict: per-requirement coverage buckets with reasons, gap states, and violations. gate_passes is the verdict.",
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
		Name:        "pin",
		Description: "Backfill binding content and shape pins to current values; returns rewritten record files.",
	}, s.toolPin)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "dispose",
		Description: "Apply a spec-change disposition: kind editorial (re-pin after meaning-preserving edit), retire (tombstone a removed identity), or supersede (tombstone sources, retarget bindings to declaring successors).",
	}, s.toolDispose)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "context",
		Description: "Code-context facts for requirement ids (comma-separated): seed symbols from the closure's bindings, and the declarations their code slice reaches. Facts only — selection is yours.",
	}, s.toolContext)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "partitions",
		Description: "Candidate work partitions for requirement ids (comma-separated; empty means all red requirements): closure-connected components with seeds, touched packages, and pairwise overlaps. Disjoint components can fan out in parallel.",
	}, s.toolPartitions)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "harden",
		Description: "Mutation-test bound implementations against the union of witnesses of every requirement each symbol implements: reqs/symbols scope (comma-separated, empty = all), per-symbol budget. Survivors are findings — strengthen a test or attest equivalence; never a gate input. Writes per-symbol kill-sheets under .stipulator/hardening/, pinned to body hash and witness set.",
	}, s.toolHarden)
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
	srv.AddResource(&mcp.Resource{
		URI:         "stipulator://coverage",
		Name:        "coverage",
		Description: "The coverage report: buckets, reasons, gap states, gate verdict. Runs the test suite.",
		MIMEType:    "application/json",
	}, s.readResource)

	// The requirement index: one listed resource per requirement, so
	// resources/list is spec browsing. Synced at startup and on every
	// successful operation, so the list is fresh as of the most recent
	// operation; reads themselves always recompile.
	s.srv = srv
	s.indexed = map[string]bool{}
	if spec, diags, err := compile.Compile(s.fsys()); err == nil && len(diags) == 0 {
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
	if len(diags) > 0 {
		msgs := make([]string, 0, len(diags))
		for _, d := range diags {
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

type compileOut struct {
	Diagnostics  []string `json:"diagnostics"`
	Requirements int      `json:"requirements"`
	Terms        int      `json:"terms"`
	Edges        int      `json:"edges"`
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
		out.Requirements = len(spec.GetRequirements())
		out.Terms = len(spec.GetTerms())
		out.Edges = len(spec.GetEdges())
	}
	return nil, out, nil
}

type verifyIn struct {
	NoTest bool `json:"no_test,omitempty" jsonschema:"skip running tests (no witnesses)"`
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
	_, rep, _, err := s.verifyPipeline(in.NoTest)
	if err != nil {
		return nil, nil, err
	}
	return protoJSON(rep.Proto())
}

func (s *Server) toolGate(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, map[string]any, error) {
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
	return protoJSON(cov.Proto())
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
	up, err := author.Gap(s.fsys(), g)
	if err != nil {
		return nil, writeOut{}, err
	}
	if err := s.write(up.Path, up.Content); err != nil {
		return nil, writeOut{}, err
	}
	return nil, writeOut{Wrote: []string{up.Path}}, nil
}

func (s *Server) toolPin(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, writeOut, error) {
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

type hardenIn struct {
	Reqs    string `json:"reqs,omitempty" jsonschema:"comma-separated requirement identifiers; empty means all bound"`
	Symbols string `json:"symbols,omitempty" jsonschema:"comma-separated implementation symbols filter"`
	Budget  int    `json:"budget,omitempty" jsonschema:"mutant budget per symbol; 0 means all, default 24"`
	Force   bool   `json:"force,omitempty" jsonschema:"rerun targets whose kill-sheet pins (body hash, witness set, operator set) still match"`
	Jobs    int    `json:"jobs,omitempty" jsonschema:"concurrent mutant runs; 0 means half the CPUs"`
}

func (s *Server) toolHarden(ctx context.Context, req *mcp.CallToolRequest, in hardenIn) (*mcp.CallToolResult, map[string]any, error) {
	spec, err := s.compileFresh()
	if err != nil {
		return nil, nil, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, nil, err
	}
	rep, err := s.harden(ctx, spec, store, in)
	if err != nil {
		return nil, nil, err
	}
	for path, content := range rep.Records(store) {
		if err := s.write(path, content); err != nil {
			return nil, nil, err
		}
	}
	out := &stipulatorv1.HardenReport{}
	var results []*stipulatorv1.HardenResult
	for _, res := range rep.Results {
		hr := &stipulatorv1.HardenResult{}
		rec := &stipulatorv1.Hardening{}
		rec.SetBackend("go")
		rec.SetSymbol(res.Symbol)
		rec.SetWitnesses(res.Witnesses)
		rec.SetOperators(golang.OperatorSet)
		var attested []*stipulatorv1.MutationAttestation
		for _, a := range res.Attested {
			ma := &stipulatorv1.MutationAttestation{}
			ma.SetPosition(a.Position)
			ma.SetOperator(a.Operator)
			ma.SetReason(a.Reason)
			attested = append(attested, ma)
		}
		rec.SetAttested(attested)
		rec.SetBodyHash(res.BodyHash)
		rec.SetMutants(int32(res.Mutants))
		rec.SetKilled(int32(res.Killed))
		var survivors []*stipulatorv1.MutationSurvivor
		for _, sv := range res.Survivors {
			m := &stipulatorv1.MutationSurvivor{}
			m.SetPosition(sv.Position)
			m.SetOperator(sv.Operator)
			survivors = append(survivors, m)
		}
		rec.SetSurvivors(survivors)
		hr.SetRecord(rec)
		hr.SetCached(res.Cached)
		hr.SetSkippedNoTests(res.SkippedNoTest)
		hr.SetSkippedNotFunction(res.SkippedNotFunc)
		results = append(results, hr)
	}
	out.SetResults(results)
	return protoJSON(out)
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

type contextIn struct {
	Ids string `json:"ids" jsonschema:"comma-separated requirement identifiers"`
}

func (s *Server) toolContext(ctx context.Context, req *mcp.CallToolRequest, in contextIn) (*mcp.CallToolResult, map[string]any, error) {
	spec, err := s.compileFresh()
	if err != nil {
		return nil, nil, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, nil, err
	}
	backends, err := s.backends()
	if err != nil {
		return nil, nil, err
	}
	ids, err := splitIDs(in.Ids)
	if err != nil {
		return nil, nil, err
	}
	seeds, decls, err := facts.Context(spec, store, backends, ids)
	if err != nil {
		return nil, nil, err
	}
	return protoJSON(facts.ContextProto(seeds, decls))
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
	case uri == "stipulator://coverage":
		spec, rep, store, err := s.verifyPipeline(false)
		if err != nil {
			return nil, err
		}
		pol, perr := s.policy()
		if perr != nil {
			return nil, perr
		}
		cov := coverage.Evaluate(spec, rep, store, true, pol)
		// Round-trip through a map for deterministic key-sorted JSON:
		// protojson output whitespace is deliberately unstable.
		_, m, err := protoJSON(cov.Proto())
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		return textResource(uri, "application/json", string(b)), nil
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
