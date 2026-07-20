package mcpserver

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/stipulate"
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

// The targets export writes the identical document under
// .stipulator/exports/ and returns only its location; a path outside
// the record-store home is refused (REQ-mcp-tools,
// REQ-mcp-writes-confined).
//
//gofresh:pure
func TestTargetsToolExportPath(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-mcp-writes-confined")
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "targets", Arguments: map[string]any{
		"export_path": ".stipulator/exports/surfaces.json",
	}})
	if err != nil || res.IsError {
		t.Fatalf("targets export: %v %+v", err, res)
	}
	doc, ok := writes[".stipulator/exports/surfaces.json"]
	if !ok || !strings.Contains(string(doc), "binding-surfaces") {
		t.Fatalf("export not written: %s", doc)
	}
	// Only the location rides the wire — not an inline copy.
	if payload := toolPayload(t, res); strings.Contains(payload, "binding-surfaces") || !strings.Contains(payload, "exported") {
		t.Fatalf("export result carries the document inline: %s", payload)
	}

	for _, bad := range []string{"/tmp/out.json", "../out.json", "docs/out.json", ".stipulator/exports/../../x.json"} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "targets", Arguments: map[string]any{
			"export_path": bad,
		}})
		if err != nil || !res.IsError {
			t.Fatalf("export path %q accepted", bad)
		}
	}
}

// Every tool outside a corpus fails with the CLI's guided message —
// the upward search and the init pointer — never a raw open error
// (REQ-mcp-server).
//
//gofresh:pure
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
