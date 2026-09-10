// Package mcpserver exposes stipulator over the Model Context Protocol:
// the compiled corpus as resources, the operations as tools.
//
// Every read serves fresh state — the corpus is recompiled and records
// reloaded per request — and all writes are confined to the record stores
// under .stipulator/: the server never edits spec documents or source —
// with one exception, the enforcement pointers a retarget's symbol
// rename moved (REQ-change-enforcement-pointers) —
// code. Tool results carry the report messages as JSON.
package mcpserver

import (
	guidancepkg "github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/remedy"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/verifyrun"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/internal/wire"
)

// Server serves one repository. The function fields exist so tests can
// inject trees, backends, and test runs; New wires production behavior.
// serverInstructions teach an agent which tool answers which question,
// so tool selection needs no trial calls (REQ-mcp-server). They are
// the embedded guidance document's decision map, verbatim — the
// single source (REQ-mcp-guidance); depth per verb is one guidance
// call away.
var serverInstructions = guidanceOrientation()

// guidanceDoc is the embedded guidance document; a malformed document
// is a build defect the parse-pinning test surfaces, so consumers
// fail loudly rather than serving nothing.
func guidanceDoc() *guidancepkg.Document {
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		panic("mcpserver: embedded guidance document malformed: " + err.Error())
	}
	return doc
}

func guidanceOrientation() string { return guidanceDoc().Orientation() }

type Server struct {
	// root is the launch directory the corpus search started from,
	// kept for guided failure messages.
	root string
	// applyMu serializes record applies: the SDK dispatches tool calls
	// concurrently, and CAS is only sound when check-then-write is one
	// critical section per process.
	applyMu  sync.Mutex
	srv      *mcp.Server
	indexed  map[string]bool
	fsys     func() fs.FS
	backends func(context.Context, []string) (map[string]verify.Backend, error)
	// capture is the one derivation of the accepted policy a witnessed
	// tool makes (REQ-check-derivation); a test substitutes it as it
	// substitutes the run.
	capture func(context.Context) (*golang.Capture, error)
	// runTests is the one witnessing surface (REQ-core-one-execution); a
	// nil scope is the whole-tree selective run, a non-nil scope narrows
	// the stale-remainder execution to the named subjects.
	runTests func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error)
	runCheck func(context.Context, bool, []string) (*stipulatorv1.CheckResult, error)
	explain  func(ctx context.Context, pkgPath, symbol string) (gofresh.Chain, string, error)
	write    func(path string, content []byte, document bool) error
	remove   func(path string) error
}

// New returns a server rooted at dir.
func New(dir string) *Server {
	return &Server{
		root: dir,
		fsys: func() fs.FS { return os.DirFS(dir) },
		backends: func(ctx context.Context, symbols []string) (map[string]verify.Backend, error) {
			return makeBackends(ctx, dir, symbols)
		},
		capture: func(ctx context.Context) (*golang.Capture, error) { return golang.LoadCapture(ctx, dir) },
		runTests: func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool) (*verify.TestRun, error) {
			if seeding == nil {
				// The witness run classifies through the tool's own
				// verification backend; without one it must not guess.
				return nil, errors.New("witness run without the tool's resolver child")
			}
			if scope == nil {
				return golang.RunWitnessesPolicy(ctx, pc, seeding)
			}
			return golang.RunWitnessesScoped(ctx, pc, scope, seeding)
		},
		runCheck: func(ctx context.Context, full bool, scopeIds []string) (*stipulatorv1.CheckResult, error) {
			return check.Run(ctx, dir, full, scopeIds)
		},
		explain: func(ctx context.Context, pkgPath, symbol string) (gofresh.Chain, string, error) {
			return golang.Explain(ctx, dir, pkgPath, symbol)
		},
		write: func(path string, content []byte, document bool) error {
			// The server is corpus-bound and its writes stay under
			// .stipulator/ (REQ-mcp-writes-confined) — with the one
			// exception, a corpus document's pointer rewrite — asserted
			// at the one seam every write passes, not per call site.
			if err := admitWrite(os.DirFS(dir), path, document); err != nil {
				return err
			}
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			// Staged like the CLI applier (REQ-record-cas): the dot-temp
			// is invisible to the record loader, and the rename is atomic,
			// so a crash mid-write never leaves a torn record.
			tmp, err := os.CreateTemp(filepath.Dir(full), ".stipulator-apply-*")
			if err != nil {
				return err
			}
			if _, err := tmp.Write(content); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return err
			}
			if err := tmp.Close(); err != nil {
				os.Remove(tmp.Name())
				return err
			}
			if err := os.Rename(tmp.Name(), full); err != nil {
				os.Remove(tmp.Name())
				return err
			}
			return nil
		},
		remove: func(path string) error {
			return os.Remove(filepath.Join(dir, filepath.FromSlash(path)))
		},
	}
}

// makeBackends prepares a tool's verification backend: served over the
// operation's symbol set (resolutions proven fresh serve, the owned
// child opens only for the stale remainder —
// REQ-evidence-resolution-freshness); a declaration-reading tool passes
// no symbols and reaches the whole-tree child through the same backend.
func makeBackends(ctx context.Context, dir string, symbols []string) (map[string]verify.Backend, error) {
	return golang.Backends(ctx, dir, symbols)
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
		return fmt.Errorf("not inside a stipulator repository (no %s under %s, searched upward at server start); run `%s` to scaffold one", corpus.ManifestPath, where, remedy.Init())
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

	mcp.AddTool(srv, knobbedTool[struct{}]("compile"), guarded(s, s.toolCompile))
	mcp.AddTool(srv, knobbedTool[verifyIn]("verify"), guarded(s, s.toolVerify))
	mcp.AddTool(srv, knobbedTool[gateIn]("gate"), guarded(s, s.toolGate))
	mcp.AddTool(srv, knobbedTool[checkIn]("check"), guarded(s, s.toolCheck))
	mcp.AddTool(srv, knobbedTool[bindIn]("bind"), guarded(s, s.toolBind))
	mcp.AddTool(srv, knobbedTool[unbindIn]("unbind"), guarded(s, s.toolUnbind))
	mcp.AddTool(srv, knobbedTool[gapIn]("gap"), guarded(s, s.toolGap))
	mcp.AddTool(srv, knobbedTool[attestRequirementIn]("attest_requirement"), guarded(s, s.toolAttestRequirement))
	mcp.AddTool(srv, knobbedTool[pinIn]("pin"), guarded(s, s.toolPin))
	mcp.AddTool(srv, knobbedTool[disposeIn]("dispose"), guarded(s, s.toolDispose))
	mcp.AddTool(srv, knobbedTool[retargetIn]("retarget"), guarded(s, s.toolRetarget))
	mcp.AddTool(srv, knobbedTool[pruneIn]("prune"), guarded(s, s.toolPrune))
	mcp.AddTool(srv, knobbedTool[contextIn]("context"), guarded(s, s.toolContext))
	mcp.AddTool(srv, knobbedTool[partitionsIn]("partitions"), guarded(s, s.toolPartitions))
	mcp.AddTool(srv, knobbedTool[readSpecIn]("read_spec"), guarded(s, s.toolReadSpec))
	mcp.AddTool(srv, knobbedTool[explainIn]("explain"), guarded(s, s.toolExplain))
	// guidance serves embedded content and deliberately skips the
	// corpus guard: orientation must work before a corpus exists.
	mcp.AddTool(srv, knobbedTool[guidanceIn]("guidance"), s.toolGuidance)

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
	current := records.HashesOf(spec)
	for _, r := range spec.GetRequirements() {
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
		if !current.Known(id) {
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

// prepare gathers a tool's held inputs — corpus, records, coverage
// policy, record hygiene — before its first child process, the one
// preparation every witness-consuming tool shares with the check and
// the CLI (REQ-check-preparation); a corpus that does not compile is
// the tool's error, as compileFresh reports it.
func (s *Server) prepare() (*check.Prepared, error) {
	prepared, err := check.Prepare(s.fsys())
	if err != nil {
		return nil, err
	}
	if faults := compile.Faults(prepared.Diagnostics); len(faults) > 0 {
		msgs := make([]string, 0, len(faults))
		for _, d := range faults {
			msgs = append(msgs, d.String())
		}
		return nil, fmt.Errorf("corpus does not compile:\n%s", strings.Join(msgs, "\n"))
	}
	if s.srv != nil {
		s.syncIndex(prepared.Spec)
	}
	return prepared, nil
}

func (s *Server) compileFresh() (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(s.fsys())
	if err != nil {
		return nil, err
	}
	if faults := compile.Faults(diags); len(faults) > 0 {
		msgs := make([]string, 0, len(faults))
		for _, d := range faults {
			msgs = append(msgs, d.String())
		}
		return nil, fmt.Errorf("corpus does not compile:\n%s", strings.Join(msgs, "\n"))
	}
	if s.srv != nil {
		s.syncIndex(spec)
	}
	return spec, nil
}

// deps is the verb cores' view of this server: its injectable seams
// under the shared shape, so a test that swaps a seam swaps it for
// every core at once.
func (s *Server) deps() verbcore.Deps {
	return verbcore.Deps{
		Prepare:  s.prepare,
		Capture:  s.capture,
		Backends: s.backends,
		RunTests: func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, _ string) (*verify.TestRun, error) {
			return s.runTests(ctx, pc, seeding, scope)
		},
	}
}

// verifyPass runs the shared verification pass over the caller's
// comma-separated exact-id scope.
func (s *Server) verifyPass(ctx context.Context, noTest bool, scopeIDs string) (*check.Prepared, *verify.Report, *verify.TestRun, error) {
	ids, err := splitIDsLoose(scopeIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	return verifyrun.Run(ctx, s.deps(), noTest, ids)
}

// withStamps appends the operation's phase-timing line to the text
// content — the notification-blind client's after-the-fact record that
// slow work was work, not a hang (REQ-mcp-progress's completed-call
// fallback). One bounded line; empty reporters append nothing.
func withStamps(text string, prog *progress.Reporter) string {
	if stamps := prog.Stamps(); stamps != "" {
		return text + "\n" + stamps
	}
	return text
}

// stampedResult is withStamps for write-shaped results: the timing line
// rides the TEXT content only — never writeOut's structured Notes, which
// enumerate operation consequences (REQ-mcp-progress's text-digest-only
// carve-out).
func stampedResult(res *mcp.CallToolResult, prog *progress.Reporter) *mcp.CallToolResult {
	if stamps := prog.Stamps(); stamps != "" && len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			tc.Text += "\n" + stamps
		}
	}
	return res
}

// projected pairs a tool's text result with its structured content: the
// one ProtoJSON projection of the result message (REQ-mcp-tools).
func projected(res *mcp.CallToolResult, m proto.Message) (*mcp.CallToolResult, map[string]any, error) {
	out, err := wire.StructuredContent(m)
	if err != nil {
		return nil, nil, err
	}
	return res, out, nil
}

// summarized emits one wire encoding of the payload: the structured
// result beside a one-line text summary. Leaving Content nil would make
// the SDK serialize the whole payload a second time as text
// (REQ-mcp-response-contract).
func summarized(line string, m proto.Message) (*mcp.CallToolResult, map[string]any, error) {
	return projected(textOnly(line), m)
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
	} else if req != nil && req.Session != nil {
		// No token: progress notifications are unaddressable, so the one
		// remaining token-free channel carries a bounded liveness trace -
		// phase transitions only, as info-level log messages. The SDK
		// sends nothing unless the client has set a log level, so this is
		// free for clients that cannot consume it; a client that set a
		// level distinguishes slow work from a hang without a token
		// (REQ-mcp-progress's liveness bound). The session guard covers
		// direct in-process calls that carry no wire request.
		session := req.Session
		notifyCtx := context.WithoutCancel(ctx)
		var phases progress.PhaseTracker
		sink = progress.NonBlocking(func(e *stipulatorv1.ProgressEvent) {
			// Notes are bounded by the policy (one per executing
			// invocation, one per persisting unit), so they ride the
			// liveness channel beside the phase transitions.
			if note := e.GetNote(); note != "" {
				_ = session.Log(notifyCtx, &mcp.LoggingMessageParams{
					Level:  "info",
					Logger: "stipulator",
					Data:   fmt.Sprintf("%s (%s elapsed)", note, e.GetElapsed().AsDuration().Round(time.Second)),
				})
			}
			if !phases.Changed(e) {
				return
			}
			_ = session.Log(notifyCtx, &mcp.LoggingMessageParams{
				Level:  "info",
				Logger: "stipulator",
				Data:   fmt.Sprintf("phase %s (%s elapsed)", progress.Word(e.GetPhase()), e.GetElapsed().AsDuration().Round(time.Second)),
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
		return fmt.Errorf("%s: %w", prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE), err)
	case context.Canceled:
		// The client's cancellation: the line names the cause, the
		// phase, and what the operation kept.
		return fmt.Errorf("%s: %w", prog.SealBy(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED, "the client"), err)
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

// verificationProblems folds a report's problems into one teaching
// refusal, every problem listed — the one rendering all three
// problem-refusing tools share.
func verificationProblems(rep *verify.Report) error {
	if len(rep.Problems) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(rep.Problems))
	for _, p := range rep.Problems {
		msgs = append(msgs, p.String())
	}
	return fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n"))
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

// textOnly is the one-line Content beside a structured result — set so
// the SDK never serializes the whole payload a second time as text
// (REQ-mcp-response-contract).
func textOnly(line string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: line}}}
}

// digestRowCap bounds the action rows a text digest carries beside the
// structured payload (REQ-mcp-response-contract's bounded text digest).
const digestRowCap = 10

// digest composes the verdict line with capped action rows: a lossy
// projection for clients that expose text content only, never a second
// encoding of the payload. Truncation is counted, not silent.
func digest(line string, rows []string) string {
	omitted := 0
	if len(rows) > digestRowCap {
		omitted = len(rows) - digestRowCap
		rows = rows[:digestRowCap]
	}
	var b strings.Builder
	b.WriteString(line)
	for _, row := range rows {
		b.WriteString("\n")
		b.WriteString(row)
	}
	if omitted > 0 {
		b.WriteString(fmt.Sprintf("\n… and %d more", omitted))
	}
	return b.String()
}

// enumWord renders a proto enum constant under its type prefix as
// lower-case words: enumWord("RESOLUTION_NOT_FOUND", "RESOLUTION_") ->
// "not found". Taking the last segment instead would invert multi-word
// values ("not found" -> "found") - a red row reading healthy.
func enumWord(name, prefix string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(name, prefix), "_", " "))
}

// viewLine names one view result for the text content: the verdict line
// plus capped action rows, so a text-only client can identify what to
// repair without the structured payload (REQ-mcp-response-contract).
func viewLine(op string, m proto.Message) string {
	switch v := m.(type) {
	case *stipulatorv1.VerifySummary:
		return digest(fmt.Sprintf("verify: %d problems, %d stale, %d broken", v.GetProblems(), v.GetStale(), v.GetBroken()),
			v.GetWitnessFailureHeadings())
	case *stipulatorv1.VerifyReport:
		var rows []string
		for _, p := range v.GetProblems() {
			rows = append(rows, p.GetPath()+": "+p.GetMessage())
		}
		if len(rows) == 0 {
			for _, r := range v.GetResults() {
				rows = append(rows, fmt.Sprintf("%s ← %s [%s, %s]", r.GetRequirementId(), r.GetSymbol(), enumWord(r.GetResolution().String(), "RESOLUTION_"), enumWord(r.GetTestOutcome().String(), "TEST_OUTCOME_")))
			}
		}
		return digest(fmt.Sprintf("verify: %d problems, %d bindings", len(v.GetProblems()), len(v.GetResults())), rows)
	case *stipulatorv1.CoverageSummary:
		word := "pass"
		if !v.GetGatePasses() {
			word = "fail"
		}
		return digest(fmt.Sprintf("gate: %s, %d violations", word, len(v.GetViolations())), v.GetViolations())
	case *stipulatorv1.CoverageReport:
		word := "pass"
		if !v.GetGatePasses() {
			word = "fail"
		}
		var rows []string
		for _, r := range v.GetRequirements() {
			row := fmt.Sprintf("%s [%s]", r.GetId(), enumWord(r.GetBucket().String(), "BUCKET_"))
			if reasons := r.GetReasons(); len(reasons) > 0 {
				row += ": " + reasons[0]
			}
			rows = append(rows, row)
		}
		return digest(fmt.Sprintf("gate: %s, %d requirements, %d violations", word, len(v.GetRequirements()), len(v.GetViolations())), rows)
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
