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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/recordapply"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/verifyrun"
)

// serverInstructions teach an agent which tool answers which question,
// so tool selection needs no trial calls (REQ-mcp-server). They are
// the embedded guidance document's decision map, verbatim — the
// single source (REQ-mcp-guidance); depth per verb is one guidance
// call away.
var serverInstructions = guidanceOrientation()

// Server serves one repository. The function fields exist so tests can
// inject trees, backends, and test runs; New wires production behavior.
type Server struct {
	// root is the launch directory the corpus search started from,
	// kept for guided failure messages.
	root     string
	srv      *mcp.Server
	indexed  map[string]bool
	fsys     func() fs.FS
	backends func(context.Context, []string) (map[string]verify.Backend, error)
	// wholeTree is the backend set a declaration-reading tool runs over
	// — the whole-tree form, reading no policy and publishing nothing —
	// where backends is the serving form a verification runs over.
	wholeTree func(context.Context) (map[string]verify.Backend, error)
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
	// applier is the one record applier every server write lands
	// through (REQ-record-cas, REQ-mcp-writes-confined).
	applier *recordapply.Applier
}

// New returns a server rooted at dir.
func New(dir string) *Server {
	s := &Server{
		root: dir,
		fsys: func() fs.FS { return os.DirFS(dir) },
		// backends is the serving form a verification runs over
		// (resolutions proven fresh serve, the owned child opens only
		// for the stale remainder — REQ-evidence-resolution-freshness);
		// wholeTree the whole-tree form a declaration read takes.
		backends: func(ctx context.Context, symbols []string) (map[string]verify.Backend, error) {
			return golang.Backends(ctx, dir, symbols)
		},
		wholeTree: func(ctx context.Context) (map[string]verify.Backend, error) {
			whole, err := golang.NewWholeTree(ctx, dir)
			if err != nil {
				return nil, err
			}
			return golang.BackendSet(whole), nil
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
	}
	// The applier reads the tree the server reads: one seam, so the
	// prior an operation stamps and the precondition that checks it
	// never consult two trees.
	s.applier = recordapply.New(dir, func() fs.FS { return s.fsys() })
	return s
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

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
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
	ids, err := verbcore.SplitIDsLoose(scopeIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	return verifyrun.Run(ctx, s.deps(), noTest, ids)
}
