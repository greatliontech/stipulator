package mcpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulator "github.com/greatliontech/stipulator"
	"github.com/greatliontech/stipulator/stipulate"
)

// guidanceText is a result's single text content.
func guidanceText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content = %d parts, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want text", res.Content[0])
	}
	return tc.Text
}

// The embedded guidance document parses — a malformed document would
// panic every serving surface, so this is the loud build-time pin
// (REQ-mcp-guidance).
//
// This subject analyzes embedded content compiled into the test
// binary: its inputs are the source closure the fingerprint already
// pins, asserted pure under REQ-purity-responsibility.
//
//gofresh:pure
func TestGuidanceDocumentParses(t *testing.T) {
	if _, err := stipulator.GuidanceDocument(); err != nil {
		t.Fatal(err)
	}
}

// The wire surface and the guidance document cannot drift: every
// listed tool's name and every input-schema property is documented,
// in both directions, judged over the REAL ListTools result
// (REQ-mcp-guidance).
//
//gofresh:pure
func TestGuidanceCoversTheWireSurface(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := harness(t, nil)
	// The wire-level instructions ARE the decision map — pinned on the
	// initialize result, not the package var (which derives from the
	// document by construction and so pins nothing).
	if got := sess.InitializeResult().Instructions; got != doc.Orientation() {
		t.Fatalf("wire instructions diverged from the decision map:\n%q", got)
	}
	list, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string][]string{}
	for _, tool := range list.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var params []string
		for name := range schema.Properties {
			params = append(params, name)
		}
		registered[tool.Name] = params
		// The served description IS the document's one-liner —
		// identity, not resemblance (REQ-mcp-guidance).
		want, err := doc.Description("mcp", tool.Name)
		if err != nil {
			t.Errorf("tool %q: %v", tool.Name, err)
			continue
		}
		if tool.Description != want {
			t.Errorf("tool %q description diverged from the document:\nwire %q\ndoc  %q", tool.Name, tool.Description, want)
		}
	}
	if defects, err := doc.Coverage("mcp", registered); err != nil || len(defects) != 0 {
		t.Fatalf("mcp coverage: err=%v defects:\n%s", err, strings.Join(defects, "\n"))
	}
}

// The guidance tool serves the document: a verb's long section under
// its mcp spelling, the decision map for the empty verb, and a
// teaching error for an unknown verb (REQ-mcp-guidance).
//
//gofresh:pure
func TestGuidanceToolServesTheDocument(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{"verb": "check"}})
	if err != nil {
		t.Fatal(err)
	}
	long, err := doc.Long("mcp", "check")
	if err != nil {
		t.Fatal(err)
	}
	if got := guidanceText(t, res); got != long {
		t.Fatalf("guidance(check) diverged from the document:\n%q\nwant\n%q", got, long)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := guidanceText(t, res); got != doc.Orientation() {
		t.Fatalf("guidance() diverged from the decision map:\n%q", got)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{"verb": "vanished"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown verb served instead of erroring")
	}
	if got := guidanceText(t, res); !strings.Contains(got, "decision map") {
		t.Fatalf("unknown-verb error teaches nothing: %q", got)
	}
}

// Outside any corpus the guidance tool still serves — the document
// is embedded — while corpus tools return the teaching error
// (REQ-mcp-guidance). The server here carries an fsys with no
// manifest: the corpus-less state guarded() detects.
//
//gofresh:pure
func TestGuidanceToolServesOutsideACorpus(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-guidance")
	doc, err := stipulator.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{fsys: func() fs.FS { return fstest.MapFS{} }}
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(context.Background(), st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "guidance", Arguments: map[string]any{"verb": "check"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("guidance refused outside a corpus: %s", guidanceText(t, res))
	}
	long, _ := doc.Long("mcp", "check")
	if got := guidanceText(t, res); got != long {
		t.Fatalf("guidance(check) outside a corpus diverged: %q", got)
	}
	// A corpus tool teaches instead.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "compile", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(guidanceText(t, res), "stipulator init") {
		t.Fatalf("corpus tool outside a corpus: %v %q", res.IsError, guidanceText(t, res))
	}
}
