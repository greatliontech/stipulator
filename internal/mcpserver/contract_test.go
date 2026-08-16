package mcpserver

import (
	"context"
	"io/fs"
	"strings"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The bind tool authors many claims in one call, all-or-nothing: two
// claims landing in one file merge, and a failure anywhere authors
// nothing (REQ-mcp-tools).
//
//gofresh:pure
func TestBindToolBatchClaims(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	sess, writes := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"claims": []map[string]any{
			{"requirement": "REQ-m-a", "symbol": "example.com/p.TestA", "role": "tests"},
			{"requirement": "REQ-m-b", "symbol": "example.com/p.F", "role": "implements"},
		},
	}})
	if err != nil || res.IsError {
		t.Fatalf("bind batch: %v %+v", err, res)
	}
	// Both land in .stipulator/bindings/m.textproto (second id segment):
	// the same-file merge is the batch's whole point.
	c, ok := writes[".stipulator/bindings/m.textproto"]
	if !ok || !strings.Contains(string(c), "REQ-m-a") || !strings.Contains(string(c), "REQ-m-b") {
		t.Fatalf("batch claims did not merge into one file:\n%s", c)
	}

	// A failure mid-batch authors nothing.
	sess2, writes2 := harness(t, nil)
	res, err = sess2.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"claims": []map[string]any{
			{"requirement": "REQ-m-a", "symbol": "example.com/p.TestA", "role": "tests"},
			{"requirement": "REQ-m-ghost", "symbol": "example.com/p.F", "role": "implements"},
		},
	}})
	if err != nil || !res.IsError {
		t.Fatalf("mid-batch failure did not error: %v %+v", err, res)
	}
	if len(writes2) != 0 {
		t.Fatalf("failed batch wrote records: %v", writes2)
	}

	// Claims and the single-claim fields are mutually exclusive.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-a", "symbol": "example.com/p.TestA", "role": "tests",
		"claims": []map[string]any{{"requirement": "REQ-m-b", "symbol": "example.com/p.F", "role": "implements"}},
	}})
	if err != nil || !res.IsError {
		t.Fatalf("mixed forms did not error: %v %+v", err, res)
	}
}

func TestToolsOutsideCorpusGuideToInit(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-server")
	sess := bareHarness(t)
	for _, call := range []*mcp.CallToolParams{
		{Name: "compile", Arguments: map[string]any{}},
		{Name: "check", Arguments: map[string]any{}},
		{Name: "gap", Arguments: map[string]any{"requirement": "REQ-x", "reason": "r", "manual": "c"}},
	} {
		res, err := sess.CallTool(context.Background(), call)
		if err != nil || !res.IsError {
			t.Fatalf("%s outside a corpus did not error: %v %+v", call.Name, err, res)
		}
		text := toolText(t, res)
		if !strings.Contains(text, "not inside a stipulator repository") || !strings.Contains(text, "stipulator init") {
			t.Fatalf("%s error lacks the guided message: %s", call.Name, text)
		}
	}
}

// bareHarness is a server rooted outside any corpus: no manifest, no
// documents — the root-guard fixture.
func bareHarness(t *testing.T) *mcp.ClientSession {
	t.Helper()
	s := &Server{
		root: "/nowhere/in/particular",
		fsys: func() fs.FS { return fstest.MapFS{} },
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(context.Background(), st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

// The server declares instructions teaching tool selection
// (REQ-mcp-server).
//
//gofresh:pure
func TestServerDeclaresInstructions(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-server")
	for _, want := range []string{"check", "progress token", ".stipulator/"} {
		if !strings.Contains(serverInstructions, want) {
			t.Fatalf("instructions lack %q", want)
		}
	}
}

// Context and partitions share the export valve: the full document
// lands under .stipulator/exports/ with only its location on the wire —
// and the partitions export carries the uncapped overlap set, the
// explicit-request form the capped wire default points at.
//
//gofresh:pure
func TestContextAndPartitionsExportPath(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids": "REQ-m-a", "no_test": true, "export_path": ".stipulator/exports/dossiers.json",
	}})
	if err != nil || res.IsError {
		t.Fatalf("context export: %v %+v", err, res)
	}
	doc, ok := writes[".stipulator/exports/dossiers.json"]
	if !ok || !strings.Contains(string(doc), "REQ-m-a") {
		t.Fatalf("context export not written: %s", doc)
	}
	if payload := toolPayload(t, res); strings.Contains(payload, "dossiers\":") || !strings.Contains(payload, "exported") {
		t.Fatalf("context export result carries the document inline: %s", payload)
	}
	// The export form is a completed suite-running call like any other:
	// its result text carries the phase-timing stamps line
	// (REQ-mcp-progress's notification-blind fallback).
	if text := toolText(t, res); !strings.Contains(text, "took ") {
		t.Fatalf("context export result missing the phase stamps: %s", text)
	}

	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "partitions", Arguments: map[string]any{
		"ids": "REQ-m-a", "no_test": true, "export_path": ".stipulator/exports/partitions.json",
	}})
	if err != nil || res.IsError {
		t.Fatalf("partitions export: %v %+v", err, res)
	}
	if _, ok := writes[".stipulator/exports/partitions.json"]; !ok {
		t.Fatal("partitions export not written")
	}
}

// The server-side applier enforces the same compare-and-swap: a record
// that moved between the operation's read and the apply refuses the
// whole batch (REQ-record-cas).
//
//gofresh:pure
func TestServerApplyCompareAndSwap(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	mem := fstest.MapFS{
		".stipulator/gaps/a.textproto": {Data: []byte("current a")},
	}
	writes := map[string][]byte{}
	s := &Server{
		fsys:   func() fs.FS { return mem },
		write:  func(p string, c []byte) error { writes[p] = c; return nil },
		remove: func(p string) error { writes[p] = nil; return nil },
	}
	if _, err := s.apply([]author.Update{
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("x"), PriorAbsent: true},
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("y"), Prior: []byte("what it read")},
	}); err == nil {
		t.Fatal("moved target accepted")
	}
	if len(writes) != 0 {
		t.Fatalf("batch partially applied despite a failed precondition: %v", writes)
	}
	out, err := s.apply([]author.Update{
		{Path: ".stipulator/gaps/a.textproto", Content: nil, Prior: []byte("current a")},
	})
	if err != nil || len(out.Deleted) != 1 {
		t.Fatalf("matching prior refused: %v %+v", err, out)
	}
}

// A tokenless client that set a log level still gets liveness: bounded
// phase-transition log messages ride the token-free logging channel, so
// slow work is distinguishable from a hang without a progress token
// (REQ-mcp-progress's liveness bound).
//
//gofresh:pure
func TestTokenlessCallEmitsPhaseLogMessages(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
		".stipulator/bindings/m.textproto": {Data: []byte(pinnedBinding(t))},
	}
	s := &Server{
		fsys: func() fs.FS { return fsys },
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/p.F":     strings.Repeat("f", 64),
				"example.com/q.TestA": strings.Repeat("q", 64),
			}}, nil
		},
		runTests: func(context.Context, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return &verify.TestRun{
				RaceEnabled:      true,
				SelectiveServing: true,
				Outcomes:         map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
			}, nil
		},
		write:  func(string, []byte) error { return nil },
		remove: func(string) error { return nil },
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() {
		_ = s.MCP().Run(context.Background(), st)
	}()
	logs := make(chan string, 64)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, r *mcp.LoggingMessageRequest) {
			if text, ok := r.Params.Data.(string); ok {
				select {
				case logs <- text:
				default:
				}
			}
		},
	})
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	if err := sess.SetLoggingLevel(context.Background(), &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("gate: %v %+v", err, res)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line := <-logs:
			if strings.Contains(line, "phase ") {
				return
			}
		case <-deadline:
			t.Fatal("no phase log message reached the tokenless client")
		}
	}
}

// The write seam itself asserts the .stipulator/ confinement
// (REQ-mcp-writes-confined) - defense in depth at the one point every
// record write passes, not a per-call-site convention.
//
//gofresh:pure
func TestWriteSeamConfinesToStipulatorDir(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-writes-confined")
	s := New(t.TempDir())
	if err := s.write("outside.txt", []byte("x")); err == nil || !strings.Contains(err.Error(), ".stipulator/") {
		t.Fatalf("out-of-home write admitted: %v", err)
	}
	if err := s.write("../escape.txt", []byte("x")); err == nil {
		t.Fatal("root-escaping write admitted")
	}
	if err := s.write(".stipulator/../escape.txt", []byte("x")); err == nil {
		t.Fatal("embedded-dotdot write admitted: the prefix held lexically while the write landed outside the home")
	}
	if err := s.write(".stipulator/gaps/ok.textproto", []byte("x")); err != nil {
		t.Fatalf("in-home write refused: %v", err)
	}
}

// Every armed tool seals its reporter on every exit: the NonBlocking
// sender goroutine ends at the terminal event, so a long-lived server
// does not strand one goroutine per pin/retarget call
// (REQ-mcp-progress).
//
//gofresh:pure
func TestPinAndRetargetSealTheirProgressReporters(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	sess, _ := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
	})
	// Amplified: one stranded sender hides inside unrelated goroutine
	// churn; sixteen do not.
	const calls = 8
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < calls; i++ {
		if res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{}}); err != nil || res.IsError {
			t.Fatalf("pin: %v %+v", err, res)
		}
		// The unresolvable replacement makes retarget refuse - the
		// error path must seal the reporter exactly like success.
		if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "retarget", Arguments: map[string]any{
			"from": "example.com/p", "to": "example.com/z", "check": true,
		}}); err != nil {
			t.Fatalf("retarget transport error: %v", err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if runtime.NumGoroutine() < before+calls {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress sender goroutines stranded: %d before, %d after %d call pairs", before, runtime.NumGoroutine(), calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The resource list is a hint, the read is the truth: a requirement
// added to the spec after the last compiling operation reads
// successfully - each read recompiles - even though no tool call has
// refreshed the listed index (REQ-mcp-resources).
//
//gofresh:pure
func TestResourceReadServesUnlistedButExistingRequirement(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-resources")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
	}
	var s *Server
	sess, _ := harnessWith(t, nil, func(srv *Server) {
		srv.fsys = func() fs.FS { return fsys }
		s = srv
	})
	_ = s
	if _, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://req/REQ-m-late"}); err == nil {
		t.Fatal("not-yet-declared requirement served")
	}
	fsys["specs/late.md"] = &fstest.MapFile{Data: []byte("# Late\n\n**REQ-m-late** (behavior): It MUST exist.\n")}
	rr, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://req/REQ-m-late"})
	if err != nil {
		t.Fatalf("unlisted-but-existing requirement refused: %v", err)
	}
	if !strings.Contains(rr.Contents[0].Text, "REQ-m-late") {
		t.Fatalf("read served the wrong document:\n%s", rr.Contents[0].Text)
	}
}
