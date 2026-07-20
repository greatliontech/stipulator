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
	pathpkg "path"
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

	surfacewire "github.com/greatliontech/stipulator/bindingsurface"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/bindingsurface"
	"github.com/greatliontech/stipulator/internal/bundle"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/dossier"
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/internal/wire"
)

// Server serves one repository. The function fields exist so tests can
// inject trees, backends, and test runs; New wires production behavior.
// serverInstructions teach an agent which tool answers which question,
// so tool selection needs no trial calls (REQ-mcp-server).
const serverInstructions = "stipulator verifies code against a compiled requirement corpus. " +
	"The loop: check answers \"does this tree pass\" (summary view by default; it serves fresh witness evidence and executes only what moved, so warm calls are cheap; full=true additionally judges suite health). " +
	"gate/verify give coverage and binding detail (summary default; views/scopes opt-in). " +
	"read_spec and context orient before writing code; partitions splits red work into disjoint components. " +
	"Authoring: bind (claims batch, all-or-nothing), gap (declare/fire/retract, batch), attest_requirement, pin (re-consent after spec edits), dispose (editorial/retire/supersede), prune (resolved records; dangling=true repairs orphans). " +
	"targets exports binding surfaces (export_path under .stipulator/exports/ for large handoffs, e.g. gomutant). " +
	"Long calls (check/gate/verify/prune/context/partitions) report phase progress when the request carries a progress token - send one and be patient rather than assuming a hang; results state the phase a deadline expired in. " +
	"All writes stay under .stipulator/; spec documents and source are never edited."

type Server struct {
	// root is the launch directory the corpus search started from,
	// kept for guided failure messages.
	root string
	srv      *mcp.Server
	indexed  map[string]bool
	fsys     func() fs.FS
	backends func(context.Context) (map[string]verify.Backend, error)
	runTests func(context.Context) (*verify.TestRun, error)
	runCheck func(context.Context, bool) (*stipulatorv1.CheckResult, error)
	write    func(path string, content []byte) error
	remove   func(path string) error
}

// New returns a server rooted at dir.
func New(dir string) *Server {
	return &Server{
		root:     dir,
		fsys:     func() fs.FS { return os.DirFS(dir) },
		backends: func(ctx context.Context) (map[string]verify.Backend, error) { return makeBackends(ctx, dir) },
		runTests: func(ctx context.Context) (*verify.TestRun, error) { return golang.RunWitnesses(ctx, dir) },
		runCheck: func(ctx context.Context, full bool) (*stipulatorv1.CheckResult, error) {
			return check.Run(ctx, dir, full)
		},
		write: func(path string, content []byte) error {
			// The server is corpus-bound: every record update must remain
			// within the tree.
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

func makeBackends(ctx context.Context, dir string) (map[string]verify.Backend, error) {
	gb, err := golang.NewOwned(ctx, dir)
	if err != nil {
		return nil, err
	}
	return map[string]verify.Backend{"go": gb}, nil
}

// Run serves MCP over stdio until the context ends.
func (s *Server) Run(ctx context.Context) error {
	return s.MCP().Run(ctx, &mcp.StdioTransport{})
}

// ensureCorpus fails a tool call before any work when the server's
// root holds no corpus, with the CLI's guided message (REQ-mcp-server):
// the upward search already ran at server start, so the guidance names
// the launch root and the init pointer instead of a raw open error.
func (s *Server) ensureCorpus() error {
	if _, err := fs.Stat(s.fsys(), corpus.ManifestPath); err != nil {
		where := s.root
		if where == "" {
			where = "."
		}
		return fmt.Errorf("not inside a stipulator repository (no %s under %s, searched upward at server start); run `stipulator init` to scaffold one", corpus.ManifestPath, where)
	}
	return nil
}

// guarded wraps a tool handler with the corpus guard, so every tool
// fails the same guided way outside a corpus.
func guarded[In, Out any](s *Server, h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		if err := s.ensureCorpus(); err != nil {
			return nil, zero, err
		}
		return h(ctx, req, in)
	}
}

// MCP builds the protocol server: tools, resource templates, and the
// requirement index.
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stipulator", Version: "v0"}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "targets",
		Description: "Derive backend-independent binding surfaces. Exact requirement, backend, and symbol arrays filter whole surfaces; the result is a structured BindingSurfaceReport. export_path (under .stipulator/exports/) writes the document to a file and returns only its location - the handoff for large surfaces (gomutant reads the same format).",
	}, guarded(s, s.toolTargets))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "compile",
		Description: "Compile the spec corpus; returns diagnostics (empty means clean) and counts.",
	}, guarded(s, s.toolCompile))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "verify",
		Description: "Check records against corpus and code. Default view is the summary (hygiene and witness counts, change signatures); view=bindings for per-binding rows, scoped with ids/filter/path. Set no_test to skip witnessing.",
	}, guarded(s, s.toolVerify))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gate",
		Description: "Coverage gate. Default view is the summary (gate_passes, counts, violations); view=reds or full for per-requirement rows; scope with ids/bucket/filter/path. Runs the test suite.",
	}, guarded(s, s.toolGate))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "check",
		Description: "One pass, one verdict: compiles the corpus, takes witness evidence — served from proven-fresh records with selective execution of only the stale remainder by default (fast on a warm tree), or one whole policy execution with full=true, which additionally judges suite health — verifies bindings against that evidence, evaluates coverage and gaps, and reports prune residue. Default view is the bounded summary (verdict, counts, capped red rows, reason histograms, diagnostic headings); view=full carries the whole CheckResult with per-test maps and retained output; ids scopes coverage rows while the verdict stays global. A tree failing the check is a successful call carrying passed=false.",
	}, guarded(s, s.toolCheck))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "bind",
		Description: "Author validated binding claims: the requirement must exist, the symbol must resolve (generated files rejected), pins applied immediately. One claim via requirement/symbol/role, or many via claims=[...] validated all-or-nothing — a failure anywhere authors nothing. Errors explain what to fix.",
	}, guarded(s, s.toolBind))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "unbind",
		Description: "Remove binding claims for a requirement, optionally narrowed by symbol and role. Matching nothing is an error.",
	}, guarded(s, s.toolUnbind))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "gap",
		Description: "Declare, fire, or retract coverage gaps. Declaring takes comma-separated requirements sharing one reason and landing condition (covered/exists/manual; covered=self lands each requirement on its own coverage; manual with fired=true declares already-fired). fired=true alone marks existing gaps' manual conditions fired. retract=true deletes the records — dangling records included. Batches apply all-or-nothing.",
	}, guarded(s, s.toolGap))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "attest_requirement",
		Description: "Author the weakest evidence: a reason-carrying voucher for a requirement, content-pinned; renders the distinct attested bucket only where the policy admits it, never covered.",
	}, guarded(s, s.toolAttestRequirement))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "pin",
		Description: "Backfill unset content pins and refresh shape pins; with ids, editorially re-pin those requirements' bindings to the current clause text (re-consent). Never silent: no-ops say so.",
	}, guarded(s, s.toolPin))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "dispose",
		Description: "Apply a spec-change disposition: kind editorial (re-pin after meaning-preserving edit), retire (tombstone a removed identity), or supersede (tombstone sources, retarget bindings to declaring successors).",
	}, guarded(s, s.toolDispose))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "prune",
		Description: "Delete resolved gap records — requirement covered and any manual landing condition explicitly fired: satisfied dead weight the gate advertises. Pass check=true to report what would be pruned without deleting. Writes only under .stipulator/gaps/.",
	}, guarded(s, s.toolPrune))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "context",
		Description: "Per-requirement dossier for ids (comma-separated): clause text with kind and keyword, coverage bucket with reasons, open gap, attestation, bindings with witness class and pin freshness, and closure seeds. Pass slice=true for the code-slice declaration frontier. Facts only — selection is yours.",
	}, guarded(s, s.toolContext))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "partitions",
		Description: "Candidate work partitions for requirement ids (comma-separated; empty means all red requirements): closure-connected components with seeds, touched packages, and pairwise overlaps. Disjoint components can fan out in parallel.",
	}, guarded(s, s.toolPartitions))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_spec",
		Description: "Read the self-contained bundle for requirement ids (comma-separated): the requirements, their closure, terms, and context. Mirrors the bundle resource for clients without resource support.",
	}, guarded(s, s.toolReadSpec))

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
	// Capped; DiagnosticsOmitted counts the remainder so the truncation
	// is never silent (REQ-mcp-response-contract).
	Diagnostics        []string `json:"diagnostics"`
	DiagnosticsOmitted int      `json:"diagnostics_omitted,omitempty"`
	Requirements       *int     `json:"requirements,omitempty"`
	Terms              *int     `json:"terms,omitempty"`
	Edges              *int     `json:"edges,omitempty"`
}

// compileDiagnosticCap bounds the compile tool's diagnostic list.
const compileDiagnosticCap = 50

func (s *Server) toolCompile(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, compileOut, error) {
	spec, diags, err := compile.Compile(s.fsys())
	if err != nil {
		return nil, compileOut{}, err
	}
	out := compileOut{Diagnostics: []string{}}
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
	return textOnly(compileLine(out)), out, nil
}

// compileLine is the one-line text beside the structured compile result.
func compileLine(out compileOut) string {
	if n := len(out.Diagnostics) + out.DiagnosticsOmitted; n > 0 {
		return fmt.Sprintf("compile: %d diagnostics", n)
	}
	if out.Requirements != nil {
		return fmt.Sprintf("compile: ok, %d requirements", *out.Requirements)
	}
	return "compile: ok"
}

type verifyIn struct {
	NoTest bool   `json:"no_test,omitempty" jsonschema:"skip running tests (no witnesses)"`
	View   string `json:"view,omitempty" jsonschema:"summary (default: hygiene and witness counts with change signatures) or bindings (the per-binding rows)"`
	Ids    string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers to scope binding rows to"`
	Filter string `json:"filter,omitempty" jsonschema:"requirement-id glob over binding rows"`
	Path   string `json:"path,omitempty" jsonschema:"prefix over declaring document or symbol"`
}

func (s *Server) verifyPipeline(ctx context.Context, noTest bool) (*stipulatorv1.Spec, *verify.Report, *records.Store, error) {
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	spec, err := s.compileFresh()
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := records.Load(s.fsys())
	if err != nil {
		return nil, nil, nil, err
	}
	backends, err := s.backends(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	var tr *verify.TestRun
	if !noTest {
		rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
		tr, err = s.runTests(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	rep.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	return spec, verify.Run(spec, store, backends, tr), store, nil
}

func (s *Server) toolVerify(ctx context.Context, req *mcp.CallToolRequest, in verifyIn) (*mcp.CallToolResult, map[string]any, error) {
	ctx, prog := s.startProgress(ctx, req)
	spec, rep, _, err := s.verifyPipeline(ctx, in.NoTest)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	scope, err := scopeFrom(in.Ids, "", in.Filter, in.Path)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	m, err := views.VerifyView(rep, views.FactsFrom(spec, rep), in.View, scope)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return summarized(viewLine("verify", m), m)
}

type gateIn struct {
	View   string `json:"view,omitempty" jsonschema:"summary (default: pass/fail + counts + violations), reds (red requirements with reasons), or full (every requirement)"`
	Ids    string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers to scope to"`
	Bucket string `json:"bucket,omitempty" jsonschema:"scope to one bucket: uncovered, stale, broken, covered, exempt, attested"`
	Filter string `json:"filter,omitempty" jsonschema:"requirement-id glob, e.g. REQ-arch-*"`
	Path   string `json:"path,omitempty" jsonschema:"prefix over declaring spec document or bound symbols, e.g. docs/specs/change.md or internal/corpus"`
}

func (s *Server) toolGate(ctx context.Context, req *mcp.CallToolRequest, in gateIn) (*mcp.CallToolResult, map[string]any, error) {
	ctx, prog := s.startProgress(ctx, req)
	spec, rep, store, err := s.verifyPipeline(ctx, false)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if len(rep.Problems) > 0 {
		msgs := make([]string, 0, len(rep.Problems))
		for _, p := range rep.Problems {
			msgs = append(msgs, p.String())
		}
		return nil, nil, terminalToolError(prog, ctx, fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n")))
	}
	pol, err := s.policy()
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	cov := coverage.Evaluate(spec, rep, store, true, pol)
	scope, err := scopeFrom(in.Ids, in.Bucket, in.Filter, in.Path)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	m, err := views.CoverageView(cov, views.FactsFrom(spec, rep), in.View, scope)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return summarized(viewLine("gate", m), m)
}

// checkIn selects the check's evidence class: full demands suite
// judgment through one whole policy execution; the default serves fresh
// witnesses and selectively executes the stale remainder
// (REQ-check-verdict).
type checkIn struct {
	Full bool   `json:"full,omitempty" jsonschema:"execute the whole accepted policy and judge suite health; default serves fresh witnesses and executes only the stale remainder"`
	View string `json:"view,omitempty" jsonschema:"summary (default: verdict, counts, capped red rows, reason histograms, diagnostic headings) or full (the whole CheckResult with per-test maps and retained output)"`
	Ids  string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers scoping coverage rows, gaps, and violations; the verdict stays global"`
}

func (s *Server) toolCheck(ctx context.Context, req *mcp.CallToolRequest, in checkIn) (*mcp.CallToolResult, map[string]any, error) {
	// View and scope words are validated before the expensive pass: a
	// typo must not cost a witness run only to be refused at render time.
	ids, err := splitIDsLoose(in.Ids)
	if err != nil {
		return nil, nil, err
	}
	if _, err := views.CheckView(&stipulatorv1.CheckResult{}, in.View, nil); err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	res, err := s.runCheck(ctx, in.Full)
	if err != nil {
		// The error return is reserved for operational faults — a tree
		// failing the check is a successful call carrying passed=false.
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	cause := stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED
	if res.GetExecution() != nil && !golang.SuiteHealthy(res.GetExecution()) {
		cause = stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE
	}
	if len(res.GetWitnessDiagnostics()) > 0 {
		cause = stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE
	}
	prog.Terminal(cause)
	view, err := views.CheckView(res, in.View, ids)
	if err != nil {
		return nil, nil, err
	}
	return summarized(checkLine(res), view)
}

// checkLine is the one-line text beside the structured result: the
// verdict and the load-bearing counts, never a duplicate serialization
// (REQ-mcp-response-contract).
func checkLine(res *stipulatorv1.CheckResult) string {
	verdict := "pass"
	if !res.GetPassed() {
		verdict = "fail"
	}
	if n := len(res.GetCompileProblems()); n > 0 {
		return fmt.Sprintf("check: %s (corpus does not compile: %d problems)", verdict, n)
	}
	if res.GetPolicyProblem() != nil {
		return fmt.Sprintf("check: %s (test policy problem)", verdict)
	}
	class := "witness-evidence"
	if res.GetSuiteHealthJudged() {
		class = "health-judged"
	}
	return fmt.Sprintf("check: %s (%s; %d served, %d executed, %d uncacheable; %d violations)",
		verdict, class, res.GetTestsServed(), res.GetTestsExecuted(), res.GetTestsUncacheable(),
		len(res.GetCoverage().GetViolations()))
}

// summarized emits one wire encoding of the payload: the structured
// result beside a one-line text summary. Leaving Content nil would make
// the SDK serialize the whole payload a second time as text
// (REQ-mcp-response-contract).
func summarized(line string, m proto.Message) (*mcp.CallToolResult, map[string]any, error) {
	out, err := wire.StructuredContent(m)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: line}}}, out, nil
}

// startProgress arms one tool call's progress seam: the returned context
// carries a Reporter whose phase tracking backs terminal-cause
// attribution, and — only when the client asked, by sending a progress
// token — whose bounded events ride MCP progress notifications
// (REQ-mcp-progress). Progress never enters result payloads: the sink is
// the notification channel and nothing else. The sink is non-blocking —
// the transport write happens on NonBlocking's sender goroutine — so a
// stalled progress-consuming client costs dropped advisory events, never
// the operation's cancellability. Notifications are sent on a
// cancellation-free context because the terminal event must still reach
// the client after the request context ends.
func (s *Server) startProgress(ctx context.Context, req *mcp.CallToolRequest) (context.Context, *progress.Reporter) {
	var sink func(*stipulatorv1.ProgressEvent)
	if token := req.Params.GetProgressToken(); token != nil {
		session := req.Session
		notifyCtx := context.WithoutCancel(ctx)
		// NonBlocking's one sender goroutine calls send serially, so the
		// counter needs no lock; MCP requires the progress value to
		// increase with every notification.
		var seq float64
		sink = progress.NonBlocking(func(e *stipulatorv1.ProgressEvent) {
			b, err := protojson.Marshal(e)
			if err != nil {
				return
			}
			seq++
			_ = session.NotifyProgress(notifyCtx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Message:       string(b),
				Progress:      seq,
			})
		})
	}
	prog := progress.New(sink)
	return progress.NewContext(ctx, prog), prog
}

// terminalToolError seals a failed call's progress and names its terminal
// cause. A call that ends at a deadline or a client cancellation
// identifies the phase it died in and which of the two ended it — so a
// client can distinguish long-running work, deadline expiry,
// cancellation, and server failure without guessing (REQ-mcp-progress,
// REQ-mcp-cancellation); any other operational fault is a server
// failure and speaks for itself.
func terminalToolError(prog *progress.Reporter, ctx context.Context, err error) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE)
		return fmt.Errorf("deadline expired in the %s phase: %w", progress.Word(prog.CurrentPhase()), err)
	case context.Canceled:
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED)
		return fmt.Errorf("cancelled by the client in the %s phase: %w", progress.Word(prog.CurrentPhase()), err)
	}
	if errors.Is(err, policy.ErrRecord) {
		// A missing or invalid accepted test policy is a fact about the
		// tree, not a server fault: the unified check fails its verdict on
		// exactly this condition (REQ-check-verdict), so the tool call
		// carries the test-failure cause and names the record's path
		// beside the loader's guidance — an agent must distinguish
		// no-policy from server failure without guessing.
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE)
		return fmt.Errorf("%s: %w", policy.Path, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE)
	return err
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
	Requirement string `json:"requirement,omitempty" jsonschema:"requirement identifier (single-claim form)"`
	Symbol      string `json:"symbol,omitempty" jsonschema:"backend-scoped symbol reference (single-claim form)"`
	Role        string `json:"role,omitempty" jsonschema:"implements, tests, or proves (single-claim form)"`
	Backend     string `json:"backend,omitempty" jsonschema:"language backend (default go; shared by batch claims lacking one)"`
	File        string `json:"file,omitempty" jsonschema:"target binding file (derived when empty)"`
	Claims      []bindClaim `json:"claims,omitempty" jsonschema:"batch claims validated all-or-nothing - a failure anywhere authors nothing; alternative to the single-claim fields"`
}

type bindClaim struct {
	Requirement string `json:"requirement" jsonschema:"requirement identifier"`
	Symbol      string `json:"symbol" jsonschema:"backend-scoped symbol reference"`
	Role        string `json:"role" jsonschema:"implements, tests, or proves"`
	Backend     string `json:"backend,omitempty" jsonschema:"language backend (defaults to the call's backend, then go)"`
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

func (s *Server) toolBind(ctx context.Context, req *mcp.CallToolRequest, in bindIn) (*mcp.CallToolResult, writeOut, error) {
	defaultBackend := in.Backend
	if defaultBackend == "" {
		defaultBackend = "go"
	}
	var reqs []author.BindRequest
	switch {
	case len(in.Claims) > 0:
		if in.Requirement != "" || in.Symbol != "" || in.Role != "" || in.File != "" {
			return nil, writeOut{}, fmt.Errorf("give either claims or the single-claim fields, not both")
		}
		for _, c := range in.Claims {
			role, err := author.ParseRole(c.Role)
			if err != nil {
				return nil, writeOut{}, err
			}
			backendName := c.Backend
			if backendName == "" {
				backendName = defaultBackend
			}
			reqs = append(reqs, author.BindRequest{
				Requirement: c.Requirement, Symbol: c.Symbol, Backend: backendName,
				Role: role, File: c.File,
			})
		}
	default:
		role, err := author.ParseRole(in.Role)
		if err != nil {
			return nil, writeOut{}, err
		}
		reqs = append(reqs, author.BindRequest{
			Requirement: in.Requirement, Symbol: in.Symbol, Backend: defaultBackend,
			Role: role, File: in.File,
		})
	}
	backends, err := s.backends(ctx)
	if err != nil {
		return nil, writeOut{}, err
	}
	ups, err := author.Binds(s.fsys(), backends, reqs)
	if err != nil {
		return nil, writeOut{}, err
	}
	out := writeOut{}
	for _, up := range ups {
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		out.Wrote = append(out.Wrote, up.Path)
	}
	return out.result(), out, nil
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
	return out.result(), out, nil
}

type gapIn struct {
	Requirement string `json:"requirement" jsonschema:"requirement identifiers, comma-separated (all share the reason and landing condition)"`
	Reason      string `json:"reason,omitempty" jsonschema:"why the gap exists (required unless retracting or firing)"`
	Covered     string `json:"covered,omitempty" jsonschema:"lands when this requirement is covered (self = each requirement's own coverage)"`
	Exists      string `json:"exists,omitempty" jsonschema:"lands when this requirement exists"`
	Manual      string `json:"manual,omitempty" jsonschema:"lands on this externally judged condition, fired explicitly"`
	Fired       bool   `json:"fired,omitempty" jsonschema:"mark the manual condition fired (without manual: fire the existing gaps)"`
	Retract     bool   `json:"retract,omitempty" jsonschema:"delete the gap records instead of declaring (dangling records included)"`
}

func (s *Server) toolGap(ctx context.Context, req *mcp.CallToolRequest, in gapIn) (*mcp.CallToolResult, writeOut, error) {
	reqs, err := splitIDs(in.Requirement)
	if err != nil {
		return nil, writeOut{}, err
	}
	conditioned := in.Covered != "" || in.Exists != "" || in.Manual != "" || in.Reason != ""
	switch {
	case in.Retract:
		if conditioned || in.Fired {
			return nil, writeOut{}, fmt.Errorf("retract takes only requirements: retraction deletes the record, conditions do not apply")
		}
		ups, err := author.RetractGaps(s.fsys(), reqs)
		if err != nil {
			return nil, writeOut{}, err
		}
		out := writeOut{}
		for _, up := range ups {
			if err := s.remove(up.Path); err != nil {
				return nil, writeOut{}, err
			}
			out.Deleted = append(out.Deleted, up.Path)
		}
		return out.result(), out, nil
	case in.Fired && in.Manual == "":
		if conditioned {
			return nil, writeOut{}, fmt.Errorf("fired alone fires existing gaps; declaring a new fired gap takes manual with fired")
		}
		ups, err := author.FireGaps(s.fsys(), reqs)
		if err != nil {
			return nil, writeOut{}, err
		}
		out := writeOut{}
		for _, up := range ups {
			if err := s.write(up.Path, up.Content); err != nil {
				return nil, writeOut{}, err
			}
			out.Wrote = append(out.Wrote, up.Path)
		}
		return out.result(), out, nil
	}
	lc, lcErr := author.NewLandingCondition(in.Covered, in.Exists, in.Manual, in.Fired)
	if lcErr != nil {
		return nil, writeOut{}, lcErr
	}
	ups, notes, err := author.Gaps(s.fsys(), reqs, in.Reason, lc)
	if err != nil {
		return nil, writeOut{}, err
	}
	// A retarget is never silent: the wire result names old and new.
	out := writeOut{Notes: notes}
	for _, up := range ups {
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		out.Wrote = append(out.Wrote, up.Path)
	}
	return out.result(), out, nil
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
			return out.result(), out, nil
		}
		out.Wrote = []string{up.Path}
		if err := s.write(up.Path, up.Content); err != nil {
			return nil, writeOut{}, err
		}
		return out.result(), out, nil
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
	return out.result(), out, nil
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
		return out.result(), out, nil
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
	backends, err := s.backends(ctx)
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
	return out.result(), out, nil
}

type readSpecIn struct {
	Ids string `json:"ids" jsonschema:"comma-separated requirement identifiers"`
}

type readSpecOut struct {
	// Bytes counts the markdown carried in the text content — the one
	// wire encoding of the bundle; duplicating the whole document into
	// the structured result would double the payload
	// (REQ-mcp-response-contract).
	Bytes int `json:"bytes"`
}

func (s *Server) toolReadSpec(ctx context.Context, req *mcp.CallToolRequest, in readSpecIn) (*mcp.CallToolResult, readSpecOut, error) {
	md, err := s.bundleMarkdown(in.Ids)
	if err != nil {
		return nil, readSpecOut{}, err
	}
	return textOnly(md), readSpecOut{Bytes: len(md)}, nil
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
	return out.result(), out, nil
}

type pruneIn struct {
	Check    bool `json:"check,omitempty" jsonschema:"report which records would be pruned, deleting nothing"`
	Dangling bool `json:"dangling,omitempty" jsonschema:"delete gap records naming requirements no longer in the corpus (the bulk repair; corpus and records only, no tests)"`
}

// toolPrune deletes resolved gap records. Detecting resolution is the
// same verify+coverage the gate performs — a resolved gap is one whose
// requirement is covered with any manual landing condition explicitly
// fired — and pruning is refused on a shaky reading: a verification
// problem could misreport a bucket and prune a still-load-bearing gap.
// It writes only under .stipulator/gaps/.
func (s *Server) toolPrune(ctx context.Context, req *mcp.CallToolRequest, in pruneIn) (*mcp.CallToolResult, writeOut, error) {
	// Danglingness is a corpus-and-records fact: no witnesses, no symbol
	// resolution, and no verification gate — a dangling gap IS a
	// verification problem, so gating its repair on clean verification
	// would deadlock the repair.
	if in.Dangling {
		spec, err := s.compileFresh()
		if err != nil {
			return nil, writeOut{}, err
		}
		store, err := records.Load(s.fsys())
		if err != nil {
			return nil, writeOut{}, err
		}
		present := map[string]bool{}
		for _, r := range spec.GetRequirements() {
			present[r.GetId()] = true
		}
		prunes := author.PruneDanglingGaps(store, present)
		out := writeOut{}
		if in.Check {
			for _, up := range prunes {
				out.Notes = append(out.Notes, "dangling gap lingers: "+up.Path)
			}
			return out.result(), out, nil
		}
		for _, up := range prunes {
			if err := s.remove(up.Path); err != nil {
				return nil, writeOut{}, err
			}
			out.Deleted = append(out.Deleted, up.Path)
		}
		return out.result(), out, nil
	}
	ctx, prog := s.startProgress(ctx, req)
	spec, rep, store, err := s.verifyPipeline(ctx, false)
	if err != nil {
		return nil, writeOut{}, terminalToolError(prog, ctx, err)
	}
	if len(rep.Problems) > 0 {
		msgs := make([]string, 0, len(rep.Problems))
		for _, p := range rep.Problems {
			msgs = append(msgs, p.String())
		}
		return nil, writeOut{}, terminalToolError(prog, ctx, fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n")))
	}
	pol, err := s.policy()
	if err != nil {
		return nil, writeOut{}, terminalToolError(prog, ctx, err)
	}
	prog.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
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
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
		return out.result(), out, nil
	}
	out := writeOut{}
	for _, up := range prunes {
		if err := s.remove(up.Path); err != nil {
			return nil, writeOut{}, terminalToolError(prog, ctx, err)
		}
		out.Deleted = append(out.Deleted, up.Path)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	return out.result(), out, nil
}

type contextIn struct {
	Ids        string `json:"ids" jsonschema:"comma-separated requirement identifiers"`
	Slice      bool   `json:"slice,omitempty" jsonschema:"include the code-slice declaration frontier (the expensive leg)"`
	NoTest     bool   `json:"no_test,omitempty" jsonschema:"skip running tests (no witnesses); dossiers render from records alone"`
	ExportPath string `json:"export_path,omitempty" jsonschema:"write the dossier report to this path under .stipulator/exports/ and return only its location - the budget valve for many-id calls"`
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
	spec, vr, store, err := s.verifyPipeline(ctx, in.NoTest)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	pol, err := s.policy()
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	cr := coverage.Evaluate(spec, vr, store, !in.NoTest, pol)
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
		backends, err := s.backends(ctx)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		_, decls, err := facts.Context(spec, store, backends, ids)
		if err != nil {
			return nil, nil, terminalToolError(prog, ctx, err)
		}
		out.SetDeclarations(facts.ContextProto(nil, decls).GetDeclarations())
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	if in.ExportPath != "" {
		doc, err := protojson.Marshal(out)
		if err != nil {
			return nil, nil, err
		}
		return s.exportTo(in.ExportPath, doc, "context")
	}
	return summarized(fmt.Sprintf("context: %d dossiers", len(out.GetDossiers())), out)
}

type partitionsIn struct {
	Ids        string `json:"ids,omitempty" jsonschema:"comma-separated requirement identifiers; empty means all red requirements"`
	NoTest     bool   `json:"no_test,omitempty" jsonschema:"skip running tests (no witnesses); partitions derive from records alone"`
	ExportPath string `json:"export_path,omitempty" jsonschema:"write the full report (uncapped overlaps) to this path under .stipulator/exports/ and return only its location"`
}

func (s *Server) toolPartitions(ctx context.Context, req *mcp.CallToolRequest, in partitionsIn) (*mcp.CallToolResult, map[string]any, error) {
	if err := validExportPath(in.ExportPath); err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	spec, rep, store, err := s.verifyPipeline(ctx, in.NoTest)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	if len(rep.Problems) > 0 {
		return nil, nil, terminalToolError(prog, ctx, fmt.Errorf("verification problems; fix records first"))
	}
	backends, err := s.backends(ctx)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
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
			switch r.Bucket {
			case coverage.Uncovered, coverage.Stale, coverage.Broken:
				ids = append(ids, r.Id)
			}
		}
	}
	pr, err := facts.Partitions(spec, store, backends, ids)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	// The export carries the FULL pairwise overlap set — the explicit
	// request the capped wire default points at.
	if in.ExportPath != "" {
		doc, err := protojson.Marshal(pr.ProtoUncapped())
		if err != nil {
			return nil, nil, err
		}
		return s.exportTo(in.ExportPath, doc, "partitions")
	}
	m := pr.Proto()
	return summarized(fmt.Sprintf("partitions: %d components, %d overlaps (%d omitted)", len(m.GetComponents()), len(m.GetOverlaps()), m.GetOverlapsOmitted()), m)
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

// exportTo validates a caller-named export path and writes the document
// under .stipulator/exports/ — the record-store home bounds every
// server write (REQ-mcp-writes-confined) — returning the location-only
// wire result.
func (s *Server) exportTo(exportPath string, doc []byte, what string) (*mcp.CallToolResult, map[string]any, error) {
	if err := validExportPath(exportPath); err != nil {
		return nil, nil, err
	}
	if err := s.write(exportPath, doc); err != nil {
		return nil, nil, err
	}
	out := map[string]any{"exported": exportPath, "bytes": len(doc)}
	return textOnly(fmt.Sprintf("%s: exported %d bytes to %s", what, len(doc), exportPath)), out, nil
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

// textOnly is the one-line Content beside a structured result — set so
// the SDK never serializes the whole payload a second time as text
// (REQ-mcp-response-contract).
func textOnly(line string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: line}}}
}

// viewLine names one view result for the text line.
func viewLine(op string, m proto.Message) string {
	switch v := m.(type) {
	case *stipulatorv1.VerifySummary:
		return fmt.Sprintf("verify: %d problems, %d stale, %d broken", v.GetProblems(), v.GetStale(), v.GetBroken())
	case *stipulatorv1.CoverageSummary:
		word := "pass"
		if !v.GetGatePasses() {
			word = "fail"
		}
		return fmt.Sprintf("gate: %s, %d violations", word, len(v.GetViolations()))
	}
	return op + " (structured content carries the payload)"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

type targetsIn struct {
	Requirements []string `json:"requirements,omitempty" jsonschema:"exact implementing requirement identifiers; alternatives"`
	Backends     []string `json:"backends,omitempty" jsonschema:"exact implementation backends; alternatives"`
	Symbols      []string `json:"symbols,omitempty" jsonschema:"exact implementation symbols; alternatives"`
	ExportPath   string   `json:"export_path,omitempty" jsonschema:"write the binding-surfaces document to this path under .stipulator/exports/ and return only its location - the artifact handoff for large surfaces (e.g. gomutant's targets input)"`
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
	report, err := bindingsurface.Derive(spec, store)
	if err != nil {
		return nil, nil, err
	}
	report, err = bindingsurface.Filter(report, in.Requirements, in.Backends, in.Symbols)
	if err != nil {
		return nil, nil, err
	}
	doc, err := surfacewire.MarshalJSON(report)
	if err != nil {
		return nil, nil, err
	}
	// The artifact handoff: the identical document written under the
	// record-store home (REQ-mcp-writes-confined bounds every server
	// write), with only its location on the wire — a consuming tool
	// reads the file instead of an inline copy of a large surface.
	if in.ExportPath != "" {
		return s.exportTo(in.ExportPath, doc, "targets")
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return nil, nil, err
	}
	return textOnly(fmt.Sprintf("targets: %d surfaces", len(report.GetSurfaces()))), m, nil
}
