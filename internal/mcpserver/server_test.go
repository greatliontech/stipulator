package mcpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

const doc = "# T\n\n**widget** (term): a gadget.\n\n" +
	"**REQ-m-a** (behavior): Using the widget it MUST x.\n\n" +
	"**REQ-m-b** (behavior): It MUST y.\n"

// pinnedBinding builds a fully pinned tests-role binding for REQ-m-a
// against the fixture corpus, so it grants a witness rather than reading
// stale.
func pinnedBinding(t *testing.T) string {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	hash := ""
	for _, r := range spec.GetRequirements() {
		if r.GetId() == "REQ-m-a" {
			hash = r.GetContentHash()
		}
	}
	return "bindings {\n  requirement_id: \"REQ-m-a\"\n  content_hash: \"" + hash +
		"\"\n  backend: \"go\"\n  symbol: \"example.com/p.TestA\"\n  role: BINDING_ROLE_TESTS\n  shape_hash: \"" +
		strings.Repeat("s", 64) + "\"\n}\n"
}

type fakeBackend map[string]string

func (f fakeBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	shape, ok := f[symbol]
	if !ok {
		return verify.NotFound, "", nil
	}
	return verify.Resolved, shape, nil
}

// harness builds a test server over an in-memory tree with captured writes.
func harness(t *testing.T, files map[string]string) (*mcp.ClientSession, map[string][]byte) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
	}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	writes := map[string][]byte{}
	s := &Server{
		fsys: func() fs.FS { return fsys },
		backends: func() (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/p.F":     strings.Repeat("f", 64),
			}}, nil
		},
		runTests: func() (*verify.TestRun, error) {
			return &verify.TestRun{
				RaceEnabled: true,
				Outcomes:    map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
			}, nil
		},
		write: func(path string, content []byte) error {
			writes[path] = content
			return nil
		},
		remove: func(path string) error {
			writes[path] = nil
			return nil
		},
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() {
		_ = s.MCP().Run(context.Background(), st)
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, writes
}

func TestResourceIndexAndReads(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-resources", "REQ-mcp-server")
	sess, _ := harness(t, nil)

	list, err := sess.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := map[string]bool{}
	for _, r := range list.Resources {
		uris[r.URI] = true
	}
	for _, want := range []string{"stipulator://req/REQ-m-a", "stipulator://req/REQ-m-b", "stipulator://coverage"} {
		if !uris[want] {
			t.Fatalf("resource list missing %s: %v", want, uris)
		}
	}

	rr, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://req/REQ-m-a"})
	if err != nil {
		t.Fatal(err)
	}
	md := rr.Contents[0].Text
	if !strings.Contains(md, "**REQ-m-a**") || !strings.Contains(md, "content_hash:") {
		t.Fatalf("requirement resource lacks source or hash:\n%s", md)
	}

	rr, err = sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://bundle/REQ-m-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.Contents[0].Text, "widget") {
		t.Fatalf("bundle resource lacks the used term:\n%s", rr.Contents[0].Text)
	}

	rr, err = sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://term/widget"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.Contents[0].Text, "gadget") {
		t.Fatalf("term resource wrong:\n%s", rr.Contents[0].Text)
	}

	if _, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://req/REQ-m-ghost"}); err == nil {
		t.Fatal("unknown requirement resource served")
	}
}

func TestGateTool(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-report-messages")
	// REQ-m-a witnessed; REQ-m-b red but gapped → gate passes.
	sess, _ := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
		".stipulator/gaps/m-b.textproto":   "requirement_id: \"REQ-m-b\"\nreason: \"later\"\nlands { attested { condition: \"x\" } }\n",
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("gate tool errored: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out struct {
		GatePasses bool `json:"gatePasses"`
		Requirements []struct {
			Id     string `json:"id"`
			Bucket string `json:"bucket"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.GatePasses {
		t.Fatalf("gate should pass: %s", b)
	}
	buckets := map[string]string{}
	for _, r := range out.Requirements {
		buckets[r.Id] = r.Bucket
	}
	if buckets["REQ-m-a"] != "BUCKET_COVERED" {
		t.Fatalf("REQ-m-a bucket = %s", buckets["REQ-m-a"])
	}
	if buckets["REQ-m-b"] != "BUCKET_UNCOVERED" {
		t.Fatalf("REQ-m-b bucket = %s", buckets["REQ-m-b"])
	}

	// The failing direction must survive the wire too: undeclared red →
	// gatePasses false with the violation named.
	sess2, _ := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
	})
	res2, err := sess2.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}})
	if err != nil || res2.IsError {
		t.Fatalf("gate: %v %v", err, res2)
	}
	b2, _ := json.Marshal(res2.StructuredContent)
	var out2 struct {
		GatePasses bool     `json:"gatePasses"`
		Violations []string `json:"violations"`
	}
	if err := json.Unmarshal(b2, &out2); err != nil {
		t.Fatal(err)
	}
	if out2.GatePasses || len(out2.Violations) != 1 || out2.Violations[0] != "REQ-m-b" {
		t.Fatalf("failing verdict lost on the wire: %s", b2)
	}
}

func TestBindToolWritesConfined(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-writes-confined")
	sess, writes := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-b", "symbol": "example.com/p.F", "role": "implements",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("bind errored: %v", res.Content[0])
	}
	if len(writes) == 0 {
		t.Fatal("no write captured")
	}
	for p := range writes {
		if !strings.HasPrefix(p, ".stipulator/") {
			t.Fatalf("write escaped the record stores: %s", p)
		}
	}

	// Confinement: file overrides must not escape the record stores.
	for _, escape := range []string{"specs/a.md", "../evil.textproto", ".stipulator/bindings/../../x.textproto", ".stipulator/gaps/x.textproto", ".stipulator/bindings/x.md"} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
			"requirement": "REQ-m-b", "symbol": "example.com/p.F", "role": "implements", "file": escape,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("file escape accepted: %s", escape)
		}
	}
	// A typo'd backend must not author an unvalidated binding.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-b", "symbol": "example.com/p.Ghost", "role": "implements", "backend": "gp",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown backend accepted")
	}

	// Teaching error: unknown requirement is a tool error, not a write.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "bind", Arguments: map[string]any{
		"requirement": "REQ-m-ghost", "symbol": "example.com/p.F", "role": "implements",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown requirement accepted")
	}
}

// TestToolListExact pins REQ-mcp-tools at the wire: the exposed tool set
// is exactly the specced one — a dropped or extra registration fails.
func TestToolListExact(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	sess, _ := harness(t, nil)
	list, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range list.Tools {
		got[tool.Name] = true
	}
	want := []string{"compile", "verify", "gate", "bind", "unbind", "gap", "pin", "read_spec", "context", "partitions", "dispose"}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("tool %s missing from the wire list: %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tool list drifted: %v", got)
	}
}

// TestDisposeToolRetire exercises the wire deletion path: retiring an
// identity whose binding and gap records exist but whose requirement is
// gone writes the tombstone and deletes the records.
func TestDisposeToolRetire(t *testing.T) {
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/gone.textproto": "bindings {\n  requirement_id: \"REQ-m-gone\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
		".stipulator/gaps/m-gone.textproto":   "requirement_id: \"REQ-m-gone\"\nreason: \"r\"\nlands { attested { condition: \"x\" } }\n",
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "dispose", Arguments: map[string]any{
		"kind": "retire", "requirement": "REQ-m-gone",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("dispose errored: %v", res.Content)
	}
	if writes[".stipulator/tombstones.textproto"] == nil {
		t.Fatal("tombstone not written")
	}
	deleted := 0
	for p, c := range writes {
		if c == nil && (strings.Contains(p, "gone")) {
			deleted++
		}
	}
	if deleted != 2 {
		t.Fatalf("expected binding+gap deletions, got %d: %v", deleted, writes)
	}

	// Unknown kind is a teaching error.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "dispose", Arguments: map[string]any{
		"kind": "obliterate", "requirement": "REQ-m-a",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("unknown kind accepted: %v %v", err, res)
	}
}

func TestCoverageResourceStableJSON(t *testing.T) {
	sess, _ := harness(t, nil)
	read := func() string {
		rr, err := sess.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "stipulator://coverage"})
		if err != nil {
			t.Fatal(err)
		}
		return rr.Contents[0].Text
	}
	a, b := read(), read()
	if a != b {
		t.Fatal("coverage resource bytes unstable across identical reads")
	}
	if !strings.Contains(a, "\"requirements\"") || !strings.Contains(a, "\"gatePasses\"") {
		t.Fatalf("coverage resource shape: %s", a)
	}
}

func TestReadSpecToolMirrorsBundle(t *testing.T) {
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_spec", Arguments: map[string]any{
		"ids": "REQ-m-a",
	}})
	if err != nil || res.IsError {
		t.Fatalf("read_spec: %v %v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "widget") {
		t.Fatalf("read_spec lacks closure content: %s", b)
	}
}
