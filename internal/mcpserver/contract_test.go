package mcpserver

import (
	"context"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
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
		write:  func(p string, c []byte, _ bool) error { writes[p] = c; return nil },
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
		".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                       {Data: []byte(doc)},
		".stipulator/bindings/m.textproto": {Data: []byte(pinnedBinding(t))},
	}
	s := &Server{
		fsys: func() fs.FS { return fsys },
		backends: func(context.Context, []string) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/p.F":     strings.Repeat("f", 64),
				"example.com/q.TestA": strings.Repeat("q", 64),
			}}, nil
		},
		capture: func(context.Context) (*golang.Capture, error) { return nil, nil },
		runTests: func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return &verify.TestRun{
				RaceEnabled:      true,
				SelectiveServing: true,
				Outcomes:         map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
			}, nil
		},
		write:  func(string, []byte, bool) error { return nil },
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
	dir := t.TempDir()
	s := New(dir)
	if err := s.write("outside.txt", []byte("x"), false); err == nil || !strings.Contains(err.Error(), "outside .stipulator/") {
		t.Fatalf("out-of-home write admitted: %v", err)
	}
	if err := s.write("../escape.txt", []byte("x"), false); err == nil {
		t.Fatal("root-escaping write admitted")
	}
	if err := s.write(".stipulator/../escape.txt", []byte("x"), false); err == nil {
		t.Fatal("embedded-dotdot write admitted: the prefix held lexically while the write landed outside the home")
	}
	if err := s.write(".stipulator/gaps/ok.textproto", []byte("x"), false); err != nil {
		t.Fatalf("in-home write refused: %v", err)
	}
	// The one exception: a document rewrite is admitted for a document
	// the corpus names and nothing else — not a source file, not a
	// document outside the manifest, not without a manifest.
	if err := s.write("specs/a.md", []byte("x"), true); err == nil {
		t.Fatal("a document rewrite was admitted with no corpus manifest")
	}
	if err := os.WriteFile(filepath.Join(dir, ".stipulator", "manifest.textproto"), []byte("include: \"specs/**/*.md\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "a.md"), []byte("# T\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.write("specs/a.md", []byte("# T\n\nrewritten\n"), true); err != nil {
		t.Fatalf("a corpus document's rewrite refused: %v", err)
	}
	if err := s.write("main.go", []byte("package x\n"), true); err == nil {
		t.Fatal("a source file was admitted as a document rewrite")
	}
	if err := s.write("notes/b.md", []byte("x"), true); err == nil {
		t.Fatal("a document outside the manifest was admitted")
	}
	// The mark is what admits a corpus document: the same path unmarked
	// is an out-of-home write, refused as one.
	if err := s.write("specs/a.md", []byte("x"), false); err == nil || !strings.Contains(err.Error(), "outside .stipulator/") {
		t.Fatalf("an unmarked corpus-document write was not refused as out-of-home: %v", err)
	}
	// Admissibility is judged for the whole batch before any write: a
	// batch whose document the seam would refuse writes nothing — not
	// even the store file listed before it.
	mem := fstest.MapFS{
		".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
		".stipulator/bindings/m.textproto": {Data: []byte("bindings { requirement_id: \"REQ-a\" backend: \"go\" symbol: \"example.com/p.T\" role: BINDING_ROLE_TESTS }\n")},
		"specs/a.md":                       {Data: []byte("# T\n")},
		"notes/b.md":                       {Data: []byte("x\n")},
	}
	writes := map[string][]byte{}
	batch := &Server{
		fsys:   func() fs.FS { return mem },
		write:  func(p string, c []byte, _ bool) error { writes[p] = c; return nil },
		remove: func(p string) error { writes[p] = nil; return nil },
	}
	if _, err := batch.apply([]author.Update{
		{Path: ".stipulator/bindings/m.textproto", Content: []byte("rewritten\n"), Prior: mem[".stipulator/bindings/m.textproto"].Data},
		{Path: "notes/b.md", Content: []byte("y\n"), Prior: mem["notes/b.md"].Data, Document: true},
	}); err == nil || !strings.Contains(err.Error(), "not a corpus document") {
		t.Fatalf("a batch with an inadmissible document: %v; want a refusal naming it", err)
	}
	if len(writes) != 0 {
		t.Fatalf("a refused batch wrote %v; want nothing", writes)
	}
	if _, err := batch.apply([]author.Update{
		{Path: ".stipulator/bindings/m.textproto", Content: []byte("rewritten\n"), Prior: mem[".stipulator/bindings/m.textproto"].Data},
		{Path: "specs/a.md", Content: []byte("# T\n\nrewritten\n"), Prior: mem["specs/a.md"].Data, Document: true},
	}); err != nil || len(writes) != 2 {
		t.Fatalf("an admissible batch: %v, wrote %v", err, writes)
	}
	// The deletion road is under the same confinement: a delete outside
	// the home is refused before anything is written or removed.
	writes = map[string][]byte{}
	if _, err := batch.apply([]author.Update{
		{Path: ".stipulator/bindings/m.textproto", Content: []byte("again\n"), Prior: mem[".stipulator/bindings/m.textproto"].Data},
		{Path: "specs/a.md", Prior: mem["specs/a.md"].Data},
	}); err == nil || !strings.Contains(err.Error(), "outside .stipulator/") || len(writes) != 0 {
		t.Fatalf("a delete outside the home: %v, wrote %v", err, writes)
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

// The partitions export form carries the UNCAPPED overlap set while the
// wire default caps with the omission counted: the tool-seam call pair
// (Proto vs ProtoUncapped) is pinned with a small fixture by lowering
// the cap (REQ-mcp-response-contract).
//
//gofresh:pure
func TestPartitionsExportCarriesUncappedOverlaps(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	prior := facts.OverlapCap
	facts.OverlapCap = 0
	defer func() { facts.OverlapCap = prior }()
	sess, writes := harnessWith(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
		".stipulator/bindings/n.textproto": pinnedBindingFor(t, "REQ-m-b", "example.com/p.F", "f"),
	}, func(srv *Server) {
		// Overlaps derive from slicer-provided packages; the plain fake
		// is no slicer, so both components would carry none.
		srv.backends = func(context.Context, []string) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": slicingFake{fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/p.F":     strings.Repeat("f", 64),
			}}}, nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "partitions", Arguments: map[string]any{
		"no_test": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("partitions: %v %+v", err, res)
	}
	if payload := toolPayload(t, res); !strings.Contains(payload, "overlapsOmitted") || strings.Contains(payload, "\"overlaps\":[{") {
		t.Fatalf("capped default should omit overlaps and count them: %s", payload)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "partitions", Arguments: map[string]any{
		"no_test": true, "export_path": ".stipulator/exports/uncapped.json",
	}})
	if err != nil || res.IsError {
		t.Fatalf("partitions export: %v %+v", err, res)
	}
	doc, ok := writes[".stipulator/exports/uncapped.json"]
	if !ok || !strings.Contains(string(doc), "overlaps") || !strings.Contains(string(doc), "REQ-m-a") {
		t.Fatalf("export missing the uncapped overlap set: %s", doc)
	}
	if !strings.Contains(string(doc), "\"overlaps\":[{") || strings.Contains(string(doc), "\"overlapsOmitted\":1") {
		t.Fatalf("export must carry the overlap rows with nothing omitted: %s", doc)
	}
}

// slicingFake is fakeBackend plus a Slice answer placing every symbol
// in one shared package - the overlap fixture.
type slicingFake struct{ fakeBackend }

func (s slicingFake) Slice(symbols []string) ([]verify.Decl, error) {
	var out []verify.Decl
	for _, sym := range symbols {
		out = append(out, verify.Decl{Package: "example.com/p", Name: sym})
	}
	return out, nil
}

// The prune tool's store mode garbage-collects the corpus's witness
// store against the current obligation universe - explicit only,
// composing with no other mode (REQ-evidence-store-gc).
//
//gofresh:pure
func TestPruneToolStoreGC(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	install := func(pkg, test string) {
		t.Helper()
		// A record carries its producing group: one without is refused
		// by the loader and so collected as cost, never kept.
		if err := witnesscache.Install(root, witnesscache.Record{Group: "6772702d64696765", Package: pkg, Test: test, Outcomes: map[string]string{pkg + "." + test: "passed"}, Fingerprint: witnesscache.Fingerprint{MaximalClosure: "aa", TestVariantClosure: "bb"}}); err != nil {
			t.Fatal(err)
		}
	}
	install("example.com/p", "TestA")
	install("example.com/p", "TestDeparted")
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
		// A non-tests-role binding naming the departed symbol confers no
		// obligation: the universe is the tests-role symbols alone.
		".stipulator/bindings/impl.textproto": "bindings {\n  requirement_id: \"REQ-m-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.TestDeparted\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
	}, func(srv *Server) { srv.root = root })
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"store": true}})
	if err != nil || res.IsError {
		t.Fatalf("prune store: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "1 record variant(s) removed, 1 kept") {
		t.Fatalf("store gc result = %s", text)
	}
	if res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"store": true, "dangling": true}}); err != nil || !res.IsError {
		t.Fatalf("composed store mode admitted: %v %+v", err, res)
	}
}

// A clause rides both bind forms: on a batch claim it scopes that claim,
// on the single-claim form it scopes the one claim, and a call's clause
// beside a claims list is the mixed form the tool refuses — never a
// clause silently dropped into a whole-requirement record
// (REQ-evidence-clause-claim, REQ-evidence-claim-batch). unbind narrows
// by the claim's spelling.
//
//gofresh:pure
func TestBindToolClauseClaims(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-clause-claim")
	clauseDoc := doc + "\n**REQ-m-c** (behavior): It MUST hold:\n\n- **alpha** first\n- second\n"
	sess, writes := harness(t, map[string]string{"specs/a.md": clauseDoc})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"claims": []map[string]any{
			{"requirement": "REQ-m-c", "symbol": "example.com/p.TestA", "role": "tests", "clause": "alpha"},
			{"requirement": "REQ-m-c", "symbol": "example.com/p.F", "role": "implements", "clause": "2"},
		},
	}})
	if err != nil || res.IsError {
		t.Fatalf("bind batch with clauses: %v %+v", err, res)
	}
	c := string(writes[".stipulator/bindings/m.textproto"])
	if !strings.Contains(c, `clause_label: "alpha"`) || !strings.Contains(c, "clause_ordinal: 2") {
		t.Fatalf("batch clauses not recorded:\n%s", c)
	}
	// The call-level clause beside a claims list is the mixed form.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"clause": "alpha",
		"claims": []map[string]any{{"requirement": "REQ-m-c", "symbol": "example.com/p.TestA", "role": "tests"}},
	}})
	if err != nil || !res.IsError || !strings.Contains(toolText(t, res), "either claims or the single-claim fields") {
		t.Fatalf("call-level clause beside claims accepted: %v %+v", err, res)
	}
	// Single-claim form, and a clause the requirement does not declare.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-c", "symbol": "example.com/p.TestA", "role": "tests", "clause": "gamma",
	}})
	if err != nil || !res.IsError || !strings.Contains(toolText(t, res), "declares no clause `gamma`") {
		t.Fatalf("unknown clause accepted: %v %+v", err, res)
	}
	// The single-claim form records the clause; the same clause by its
	// ordinal for the same symbol and role is the same claim (refused).
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-c", "symbol": "example.com/p.TestA", "role": "tests", "clause": "1",
	}})
	if err != nil || !res.IsError || !strings.Contains(toolText(t, res), "identical binding") {
		t.Fatalf("the labelled clause by ordinal accepted as a second claim: %v %s", err, toolText(t, res))
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-c", "symbol": "example.com/p.TestA", "role": "tests",
	}})
	if err != nil || res.IsError {
		t.Fatalf("single-claim whole beside a clause claim: %v %s", err, toolText(t, res))
	}
	// unbind narrows by the claim's spelling: the label claim goes, the
	// whole claim on the same symbol and the other clause's claim stay.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "unbind", Arguments: map[string]any{
		"requirement": "REQ-m-c", "symbol": "example.com/p.TestA", "clause": "alpha",
	}})
	if err != nil || res.IsError {
		t.Fatalf("unbind --clause: %v %s", err, toolText(t, res))
	}
	c = string(writes[".stipulator/bindings/m.textproto"])
	if strings.Contains(c, `clause_label: "alpha"`) || !strings.Contains(c, "clause_ordinal: 2") || strings.Count(c, "example.com/p.TestA") != 1 {
		t.Fatalf("unbind by clause spelling removed the wrong claims:\n%s", c)
	}
}

// A refusal on a broken corpus carries the compile's remedies beside
// its faults on the agent surface too: a gap declared against the
// mid-disposition corpus names the one-step supersede
// (REQ-change-remediation, REQ-change-split-merge).
//
//gofresh:pure
func TestCorpusRefusalCarriesRemediesOnTheAgentSurface(t *testing.T) {
	stipulate.Covers(t, "REQ-change-remediation")
	mid := "# T\n\n**REQ-m-new** (behavior, supersedes REQ-m-gone): It MUST new.\n"
	sess, _ := harness(t, map[string]string{"specs/a.md": mid})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{
		"requirement": "REQ-m-new", "reason": "r", "manual": "later",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("gap on a broken corpus did not refuse: %v %+v", err, res)
	}
	text := toolText(t, res)
	if !strings.Contains(text, "supersedes REQ-m-gone, which is neither declared nor tombstoned") || !strings.Contains(text, "remedy: if REQ-m-gone was removed by this edit") || !strings.Contains(text, "dispose kind=supersede") {
		t.Fatalf("refusal lacks the fault or its remedy: %s", text)
	}
}
