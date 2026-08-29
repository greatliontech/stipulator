package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
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
	return pinnedBindingFor(t, "REQ-m-a", "example.com/p.TestA", "s")
}

// pinnedBindingFor is pinnedBinding for any fixture requirement and
// symbol; shapeChar repeats to the shape hash the fake backend answers
// for the symbol.
func pinnedBindingFor(t *testing.T, req, sym, shapeChar string) string {
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
		if r.GetId() == req {
			hash = r.GetContentHash()
		}
	}
	return "bindings {\n  requirement_id: \"" + req + "\"\n  content_hash: \"" + hash +
		"\"\n  backend: \"go\"\n  symbol: \"" + sym + "\"\n  role: BINDING_ROLE_TESTS\n  shape_hash: \"" +
		strings.Repeat(shapeChar, 64) + "\"\n}\n"
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
	return harnessWith(t, files, nil)
}

// harnessWith additionally lets a test override the server's injectable
// function fields before the session starts.
func harnessWith(t *testing.T, files map[string]string, mut func(*Server)) (*mcp.ClientSession, map[string][]byte) {
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
		write: func(path string, content []byte) error {
			// Captured AND fed back: the real server reads the tree it
			// writes, and read-after-write flows (pin to quiescence,
			// re-declare over an update) depend on it.
			writes[path] = content
			fsys[path] = &fstest.MapFile{Data: content}
			return nil
		},
		remove: func(path string) error {
			writes[path] = nil
			delete(fsys, path)
			return nil
		},
	}
	if mut != nil {
		mut(s)
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

func TestCanceledToolCallStopsWitnessRun(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-server")
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := &Server{
		fsys: func() fs.FS {
			return fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte(doc)},
			}
		},
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{}, nil
		},
		runTests: func(ctx context.Context, _ map[gofresh.Subject]bool) (*verify.TestRun, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		},
	}
	ct, st := mcp.NewInMemoryTransports()
	serverCtx, stopServer := context.WithCancel(context.Background())
	t.Cleanup(stopServer)
	go func() { _ = s.MCP().Run(serverCtx, st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}})
		done <- err
	}()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("witness run did not receive request cancellation")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("tool call error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool call did not return after cancellation")
	}
}

//gofresh:pure
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
	for _, want := range []string{"stipulator://req/REQ-m-a", "stipulator://req/REQ-m-b"} {
		if !uris[want] {
			t.Fatalf("resource list missing %s: %v", want, uris)
		}
	}
	// Coverage deliberately has no resource: the gate tool's views are
	// the one surface (REQ-mcp-resources).
	if uris["stipulator://coverage"] {
		t.Fatalf("pruned coverage resource still listed: %v", uris)
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

//gofresh:pure
func TestGateTool(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-report-messages")
	// REQ-m-a witnessed; REQ-m-b red but gapped → gate passes.
	sess, _ := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
		".stipulator/gaps/m-b.textproto":   "requirement_id: \"REQ-m-b\"\nreason: \"later\"\nlands { manual { condition: \"x\" } }\n",
		".gomutant/findings.json":          `{not json}`,
	})
	// Default view is the summary: pass/fail + counts + violations,
	// no per-requirement array — the answer most calls want.
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("gate tool errored: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var sum struct {
		GatePasses bool `json:"gatePasses"`
		Covered    int  `json:"covered"`
		Uncovered  int  `json:"uncovered"`
		GapsOpen   int  `json:"gapsOpen"`
	}
	if err := json.Unmarshal(b, &sum); err != nil {
		t.Fatal(err)
	}
	if !sum.GatePasses || sum.Covered != 1 || sum.Uncovered != 1 || sum.GapsOpen != 1 {
		t.Fatalf("summary wrong: %s", b)
	}
	if strings.Contains(string(b), "requirements") {
		t.Fatalf("summary carries the per-requirement array: %s", b)
	}
	if strings.Contains(string(b), "hardeningReminder") {
		t.Fatalf("gate tool retained the retired hardening reminder: %s", b)
	}

	// view=full: the per-requirement array.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{"view": "full"}})
	if err != nil || res.IsError {
		t.Fatalf("gate full: %v %v", err, res)
	}
	b, _ = json.Marshal(res.StructuredContent)
	var out struct {
		GatePasses   bool `json:"gatePasses"`
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

	// Scope: bucket=uncovered narrows the full view to the red row.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{"view": "full", "bucket": "uncovered"}})
	if err != nil || res.IsError {
		t.Fatalf("gate scoped: %v %v", err, res)
	}
	b, _ = json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Requirements) != 1 || out.Requirements[0].Id != "REQ-m-b" {
		t.Fatalf("bucket scope wrong: %s", b)
	}

	// An unknown bucket refuses — a typo must never read as empty.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{"bucket": "redish"}})
	if err != nil || !res.IsError {
		t.Fatalf("unknown bucket accepted: %v %v", err, res)
	}

	// An unknown exact identifier refuses the same way, on both scoped
	// read surfaces — scoping to a typo would otherwise serve a bare
	// zero-row answer indistinguishable from a clean scope
	// (REQ-mcp-response-contract).
	for _, tool := range []string{"gate", "verify"} {
		res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"ids": "REQ-m-vanished"}})
		if err != nil || !res.IsError {
			t.Fatalf("%s accepted an unknown id: %v %v", tool, err, res)
		}
		var text string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		if !strings.Contains(text, "unknown requirement identifier") || !strings.Contains(text, "REQ-m-vanished") {
			t.Fatalf("%s unknown-id refusal unteaching: %s", tool, text)
		}
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

//gofresh:pure
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
//
//gofresh:pure
func TestToolListExact(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	sess, _ := harness(t, nil)
	list, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	var contextDescription string
	for _, tool := range list.Tools {
		got[tool.Name] = true
		if tool.Name == "context" {
			contextDescription = tool.Description
		}
	}
	want := []string{"compile", "verify", "gate", "check", "bind", "unbind", "gap", "pin", "prune", "read_spec", "context", "partitions", "dispose", "retarget", "attest_requirement", "explain", "guidance"}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("tool %s missing from the wire list: %v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("tool list drifted: %v", got)
	}
	if !strings.Contains(contextDescription, "closure seeds") || strings.Contains(contextDescription, "hardening") {
		t.Fatalf("context description is stale: %q", contextDescription)
	}
}

// Prune's resolved-record evaluation is pinned to the serving class:
// witness evidence without the selective runner's mark refuses the
// operation instead of pruning on whole-execution evidence
// (REQ-gap-resolved-pruned).
//
//gofresh:pure
func TestPruneRefusesNonServingEvidence(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
		// A gap record, so the evaluation actually runs: a gapless tree
		// takes the deletion-only fast path and never reads evidence.
		".stipulator/gaps/m-a.textproto": {Data: []byte("requirement_id: \"REQ-m-a\"\nreason: \"pending\"\nlands {\n  manual {\n    condition: \"c\"\n  }\n}\n")},
	}
	s := &Server{
		fsys: func() fs.FS { return fsys },
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{}}, nil
		},
		runTests: func(context.Context, map[gofresh.Subject]bool) (*verify.TestRun, error) {
			// A whole-execution run: no serving-class mark.
			return &verify.TestRun{RaceEnabled: true}, nil
		},
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(context.Background(), st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("prune accepted whole-execution evidence: %+v", res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "serving-class witness evidence") {
		t.Fatalf("refusal does not name the mandated class: %q", text)
	}
}

// The retarget tool's check form runs the full validation and writes
// nothing; the applying form rewrites the store and reports every
// old-to-new identity (REQ-change-retarget).
//
//gofresh:pure
func TestRetargetToolCheckWritesNothing(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retarget")
	sess, writes := harness(t, map[string]string{".stipulator/bindings/m.textproto": pinnedBinding(t)})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "retarget", Arguments: map[string]any{
		"from": "example.com/p", "to": "example.com/q", "check": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("retarget check = %+v, %v", res, err)
	}
	if len(writes) != 0 {
		t.Fatalf("check form wrote: %v", writes)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "1 binding(s) would retarget") {
		t.Fatalf("check text = %q", text)
	}

	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "retarget", Arguments: map[string]any{
		"from": "example.com/p", "to": "example.com/q",
	}})
	if err != nil || res.IsError {
		t.Fatalf("retarget = %+v, %v", res, err)
	}
	got, ok := writes[".stipulator/bindings/m.textproto"]
	if !ok || !strings.Contains(string(got), "example.com/q.TestA") {
		t.Fatalf("applying form did not rewrite the store: %q", got)
	}
}

func TestCompileToolCounts(t *testing.T) {
	// Clean arm: counts present and correct.
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "compile", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("compile clean: %v %v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var clean struct {
		Diagnostics  []string `json:"diagnostics"`
		Requirements *int     `json:"requirements"`
		Terms        *int     `json:"terms"`
		Edges        *int     `json:"edges"`
	}
	if err := json.Unmarshal(b, &clean); err != nil {
		t.Fatal(err)
	}
	if len(clean.Diagnostics) != 0 {
		t.Fatalf("clean corpus has diagnostics: %s", b)
	}
	if clean.Requirements == nil || *clean.Requirements != 2 {
		t.Fatalf("requirements count wrong: %s", b)
	}
	if clean.Terms == nil || *clean.Terms != 1 {
		t.Fatalf("terms count wrong: %s", b)
	}
	if clean.Edges == nil {
		t.Fatalf("edges count absent on clean corpus: %s", b)
	}

	// Error arm: a keyword outside a requirement fails compilation, so there
	// is no IR. The counts must be ABSENT, never a zero that reads as
	// "nothing parsed".
	badSess, _ := harness(t, map[string]string{
		"specs/bad.md": "# Bad\n\nThe system MUST work here.\n",
	})
	res, err = badSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "compile", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("compile error arm: %v %v", err, res)
	}
	b, _ = json.Marshal(res.StructuredContent)
	var bad struct {
		Diagnostics  []string `json:"diagnostics"`
		Requirements *int     `json:"requirements"`
		Terms        *int     `json:"terms"`
		Edges        *int     `json:"edges"`
	}
	if err := json.Unmarshal(b, &bad); err != nil {
		t.Fatal(err)
	}
	if len(bad.Diagnostics) == 0 {
		t.Fatalf("erroring corpus reported no diagnostics: %s", b)
	}
	if bad.Requirements != nil || bad.Terms != nil || bad.Edges != nil {
		t.Fatalf("counts present on error arm, should be absent: %s", b)
	}
	// The false zero must not appear on the wire at all.
	for _, k := range []string{"requirements", "terms", "edges"} {
		if strings.Contains(string(b), k) {
			t.Fatalf("count key %q leaked onto error arm: %s", k, b)
		}
	}
}

// TestDisposeToolRetire exercises the wire deletion path: retiring an
// identity whose binding and gap records exist but whose requirement is
// gone writes the tombstone and deletes the records.
//
//gofresh:pure
func TestDisposeToolRetire(t *testing.T) {
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/gone.textproto": "bindings {\n  requirement_id: \"REQ-m-gone\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
		".stipulator/gaps/m-gone.textproto":   "requirement_id: \"REQ-m-gone\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n",
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

// TestPruneTool exercises the wire prune verb: a gap on a now-covered
// requirement is resolved dead weight — check=true reports it without
// deleting, and the plain call deletes exactly that record and nothing else.
//
//gofresh:pure
func TestPruneTool(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-gap-resolved-pruned")
	gapPath := ".stipulator/gaps/m-a.textproto"
	openGapPath := ".stipulator/gaps/m-b.textproto"
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t), // REQ-m-a covered
		// Resolved: its requirement is covered and the manual condition is
		// explicitly fired — an unfired manual gap stays open on a covered
		// requirement and is never prunable.
		gapPath: "requirement_id: \"REQ-m-a\"\nreason: \"was deferred\"\nlands { manual { condition: \"x\" fired: true } }\n",
		// Open: REQ-m-b is uncovered, so this gap is load-bearing and must survive.
		openGapPath: "requirement_id: \"REQ-m-b\"\nreason: \"later\"\nlands { manual { condition: \"x\" } }\n",
	})

	// check=true reports the resolved gap and deletes nothing.
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"check": true}})
	if err != nil || res.IsError {
		t.Fatalf("prune check: %v %v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "m-a.textproto") {
		t.Fatalf("check did not report the resolved gap: %s", b)
	}
	// The preview marks itself: a zero-row check and a zero-write apply
	// must never be confusable on the wire.
	if !strings.Contains(string(b), `"check":true`) {
		t.Fatalf("check preview unmarked: %s", b)
	}
	if _, touched := writes[gapPath]; touched {
		t.Fatal("check touched the gap record — must be dry-run")
	}

	// Plain call deletes exactly the resolved gap.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("prune: %v %v", err, res)
	}
	if c, ok := writes[gapPath]; !ok || c != nil {
		t.Fatalf("resolved gap not deleted (ok=%v content=%v)", ok, c)
	}
	// The open gap is load-bearing and must survive — prune deletes only
	// resolved gaps, never open ones.
	if _, touched := writes[openGapPath]; touched {
		t.Fatal("prune deleted an OPEN gap — it must delete only resolved gaps")
	}
	b, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), gapPath) {
		t.Fatalf("deletion not reported in the result: %s", b)
	}

	// Quiescence says so: with the resolved gap already pruned, another
	// pass names the empty result instead of a bare zero-write success.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("quiescent prune: %v %v", err, res)
	}
	if b, _ = json.Marshal(res.StructuredContent); !strings.Contains(string(b), "no resolved gap records linger") {
		t.Fatalf("quiescent prune said nothing: %s", b)
	}

	// The dangling mode's empty result is named too, on check and apply
	// alike, with the check marked.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "prune", Arguments: map[string]any{"dangling": true, "check": true}})
	if err != nil || res.IsError {
		t.Fatalf("dangling check: %v %v", err, res)
	}
	if b, _ = json.Marshal(res.StructuredContent); !strings.Contains(string(b), "no dangling gap records") || !strings.Contains(string(b), `"check":true`) {
		t.Fatalf("empty dangling check unmarked or silent: %s", b)
	}
}

//gofresh:pure
func TestReadSpecToolMirrorsBundle(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_spec", Arguments: map[string]any{
		"ids": "REQ-m-a",
	}})
	if err != nil || res.IsError {
		t.Fatalf("read_spec: %v %v", err, res)
	}
	// The bundle rides the structured result once - the channel
	// structured-preferring clients read - beside a size-only text
	// digest (REQ-mcp-response-contract).
	sc, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("structured content: %v", err)
	}
	var out struct {
		Spec string `json:"spec"`
	}
	if err := json.Unmarshal(sc, &out); err != nil {
		t.Fatalf("structured shape: %v: %s", err, sc)
	}
	if !strings.Contains(out.Spec, "widget") {
		t.Fatalf("structured result lacks closure content: %s", sc)
	}
	if text := toolText(t, res); strings.Contains(text, "widget") || !strings.Contains(text, "bytes") {
		t.Fatalf("text side is not the size-only digest: %s", text)
	}
}

// TestAttestTools pins the MCP surface of the requirement attest verb:
// writes land in the record store, and refusals surface as tool errors.
//
//gofresh:pure
func TestAttestTools(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-evidence-attestation")
	sess, writes := harness(t, map[string]string{
		"specs/should.md": "# S\n\n**REQ-m-s** (behavior): It SHOULD s.\n",
	})

	// A cell that never admits attestation refuses at write time with
	// its real demand (REQ-change-remediation's born-valid arm).
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "attest_requirement", Arguments: map[string]any{
		"requirement": "REQ-m-a", "reason": "judged by review",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("MUST-cell attestation accepted: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "never admits attestation") || !strings.Contains(text, "executed witness") {
		t.Fatalf("refusal lacks the cell's demand: %s", text)
	}

	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "attest_requirement", Arguments: map[string]any{
		"requirement": "REQ-m-s", "reason": "judged by review",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("attest_requirement errored: %v", res.Content[0])
	}
	found := false
	for p := range writes {
		if strings.HasPrefix(p, ".stipulator/attestations/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("requirement attestation not written: %v", writes)
	}

	// Reasonless requests surface as tool errors naming the missing
	// reason — asserted by substring so the admission refusal (REQ-m-b is
	// a MUST cell) cannot mask a vanished reason check: the SHOULD cell
	// carries the discrimination.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "attest_requirement", Arguments: map[string]any{
		"requirement": "REQ-m-s",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("reasonless attestation accepted: %v %v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "reason") {
		t.Fatalf("reasonless refusal does not name the reason: %s", text)
	}
}

// toolText flattens a tool result's text content for assertions.
func toolText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// toolPayload renders the structured result as JSON — the machine
// surface; the text content carries only a one-line summary
// (REQ-mcp-response-contract).
func toolPayload(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPinToolReportsMovedShapes pins REQ-pin-backfill's shape-side
// reporting on the wire: the ids form re-pins clause text only, so a
// shape mismatch on the named requirement's bindings is reported instead
// of an answer reading as quiescence, and the blanket form names the
// symbols whose differing shape pins it rewrote — the rewrite clears
// verification's shape-mismatch signal, the one trace that a bound
// implementation moved.
func TestPinToolReportsMovedShapes(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/shape.textproto": "bindings {\n  requirement_id: \"REQ-m-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n  shape_hash: \"" + strings.Repeat("a", 64) + "\"\n}\n",
	})
	// ids over an unset content pin: the clause text re-pins, and the
	// untouched shape mismatch is named beside the write.
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{"ids": "REQ-m-a"}})
	if err != nil || res.IsError {
		t.Fatalf("pin ids: %v %v", err, res)
	}
	text := toolPayload(t, res)
	if !strings.Contains(text, "shape of example.com/p.F moved") || !strings.Contains(text, "blanket pin (no ids) re-pins shapes") {
		t.Fatalf("ids form conceals the shape mismatch it will not fix: %s", text)
	}
	if content := writes[".stipulator/bindings/shape.textproto"]; !strings.Contains(string(content), strings.Repeat("a", 64)) {
		t.Fatalf("ids form rewrote the shape pin: %s", content)
	}

	// ids again, clause now current: still no quiescence claim while the
	// shape mismatch stands — "pins current" while verification stays red
	// on the same rows is the defect this answer replaces.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{"ids": "REQ-m-a"}})
	if err != nil || res.IsError {
		t.Fatalf("pin ids repeat: %v %v", err, res)
	}
	text = toolPayload(t, res)
	if !strings.Contains(text, "clause pins current; shape of example.com/p.F moved") {
		t.Fatalf("ids no-op beside a shape mismatch claims quiescence: %s", text)
	}

	// The blanket form rewrites the differing shape pin and says so.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("blanket pin: %v %v", err, res)
	}
	text = toolPayload(t, res)
	if !strings.Contains(text, "shape pins refreshed (bound implementation moved): example.com/p.F") {
		t.Fatalf("blanket pin cleared the shape-mismatch signal invisibly: %s", text)
	}
	if content := writes[".stipulator/bindings/shape.textproto"]; strings.Contains(string(content), strings.Repeat("a", 64)) {
		t.Fatalf("blanket pin left the differing shape pin: %s", content)
	}

	// Quiescence is once again a plain answer.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{"ids": "REQ-m-a"}})
	if err != nil || res.IsError {
		t.Fatalf("pin ids final: %v %v", err, res)
	}
	if text = toolPayload(t, res); !strings.Contains(text, "REQ-m-a: pins current") || strings.Contains(text, "moved") {
		t.Fatalf("quiescent ids answer wrong: %s", text)
	}
}

// TestPinTool pins the refresh verb's contract: ids editorially re-pin a
// stale content pin (the one-verb recovery after a reword), a clean
// requirement reports "pins current", and the no-id no-op is never a
// silent empty object.
//
//gofresh:pure
func TestPinTool(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill", "REQ-change-editorial")
	sess, writes := harness(t, map[string]string{
		".stipulator/bindings/stale.textproto": "bindings {\n  requirement_id: \"REQ-m-a\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n  content_hash: \"" + strings.Repeat("0", 64) + "\"\n}\n",
	})
	// The blanket form never rewrites the differing pin, and it names the
	// requirement awaiting re-consent in its own response — the caller
	// must not need a later staleness report to learn about it.
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("blanket pin over differing pin: %v %v", err, res)
	}
	if text := toolPayload(t, res); !strings.Contains(text, "awaiting re-consent") || !strings.Contains(text, "REQ-m-a") {
		t.Fatalf("blanket pin conceals the preserved differing pin: %s", text)
	}
	if content, ok := writes[".stipulator/bindings/stale.textproto"]; ok && !strings.Contains(string(content), strings.Repeat("0", 64)) {
		t.Fatalf("blanket pin laundered the differing content pin: %s", content)
	}

	// A repeat blanket run writes nothing, yet the differing pin still
	// awaits: the no-op wording must not claim quiescence.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("repeat blanket pin: %v %v", err, res)
	}
	if text := toolPayload(t, res); strings.Contains(text, "all pins current") || !strings.Contains(text, "no pins backfilled") {
		t.Fatalf("no-op beside a preserved differing pin misreported: %s", text)
	}

	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{
		"ids": "REQ-m-a",
	}})
	if err != nil || res.IsError {
		t.Fatalf("pin with ids: %v %v", err, res)
	}
	content, ok := writes[".stipulator/bindings/stale.textproto"]
	if !ok || strings.Contains(string(content), strings.Repeat("0", 64)) {
		t.Fatalf("stale pin not refreshed: %s", content)
	}

	// Re-pinning a current requirement is a reported no-op.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{
		"ids": "REQ-m-b",
	}})
	if err != nil || res.IsError {
		t.Fatalf("pin current: %v %v", err, res)
	}
	text := toolPayload(t, res)
	if !strings.Contains(text, "pins current") {
		t.Fatalf("no-op silent: %s", text)
	}

	// The blanket form is never a silent empty object: run it to
	// quiescence, then the no-op run must SAY it did nothing.
	if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("blanket pin: %v %v", err, res)
	}
	if text := toolPayload(t, res); !strings.Contains(text, "all pins current") {
		t.Fatalf("blanket no-op silent: %s", text)
	}
}

// TestContextDossier pins the orientation call: one request answers with
// the clause, coverage, gap, and bindings with witness class — no
// record-store spelunking; a JSON-array-encoded ids value is
// tolerated; an unknown id is quoted cleanly.
//
//gofresh:pure
func TestContextDossier(t *testing.T) {
	stipulate.Covers(t, "REQ-context-dossier")
	sess, _ := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
		".stipulator/gaps/m-b.textproto":   "requirement_id: \"REQ-m-b\"\nreason: \"awaiting design\"\nlands { manual { condition: \"design settles\" } }\n",
		// Context does not consume producer-owned findings; malformed external
		// material cannot break dossier assembly.
		".gomutant/findings.json": `{not json}`,
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids": "REQ-m-a,REQ-m-b",
	}})
	if err != nil || res.IsError {
		t.Fatalf("context: %v %v", err, res)
	}
	text := toolPayload(t, res)
	for _, want := range []string{
		`"Using the widget it MUST x."`, // clause text, compiled view
		`"bucket":"BUCKET_COVERED"`,     // REQ-m-a: pinned witness passed
		`"awaiting design"`,             // REQ-m-b's gap reason
		`"design settles"`,              // and its landing condition
		`"witnessClass":"WITNESS_CLASS_EXAMPLE"`,
		`"gapState":"GAP_STATE_OPEN"`, // the record's evaluated state
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dossier missing %s:\n%s", want, text)
		}
	}
	for _, retired := range []string{`"hardening"`, `"mutants"`, `"killed"`, `"survivors"`} {
		if strings.Contains(text, retired) {
			t.Fatalf("dossier retained mutation result field %s:\n%s", retired, text)
		}
	}

	// A store failing verification must say so in the dossier: a
	// dangling binding's problem rides the report.
	sessBad, _ := harness(t, map[string]string{
		".stipulator/bindings/ghost.textproto": "bindings {\n  requirement_id: \"REQ-m-ghost\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n",
	})
	res, err = sessBad.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids": "REQ-m-a",
	}})
	if err != nil || res.IsError {
		t.Fatalf("context over problem store: %v %v", err, res)
	}
	if text := toolPayload(t, res); !strings.Contains(text, "is not in the corpus") || !strings.Contains(text, `"problems"`) {
		t.Fatalf("verification problems hidden from the dossier: %s", text)
	}

	// JSON-array-encoded ids: tolerated, same answer.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids": `["REQ-m-a"]`,
	}})
	if err != nil || res.IsError {
		t.Fatalf("array ids rejected: %v %v", err, res)
	}

	// Unknown id: refused before the pass it would scope, the id named.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids": "REQ-m-ghost",
	}})
	if err != nil || !res.IsError {
		t.Fatal("unknown id accepted")
	}
	if msg := fmt.Sprint(res.Content[0]); !strings.Contains(msg, "unknown requirement identifier(s): REQ-m-ghost") {
		t.Fatalf("unknown id not refused cleanly: %s", msg)
	}
}

func TestContextAndPartitionsNoTestSkipWitnessing(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools")
	sess, _ := harness(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
	})
	// An unwitnessed evaluation is not a witnessed run: a test-bound
	// requirement must not read as broken merely because no_test skipped the
	// witness pipeline.
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids":     "REQ-m-a",
		"no_test": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("context no_test: %v %v", err, res)
	}
	text := toolPayload(t, res)
	if strings.Contains(text, `"BUCKET_BROKEN"`) {
		t.Fatalf("no_test dossier buckets the requirement broken:\n%s", text)
	}
	if !strings.Contains(text, `"Using the widget it MUST x."`) {
		t.Fatalf("no_test dossier lost the compiled clause:\n%s", text)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "partitions", Arguments: map[string]any{
		"ids":     "REQ-m-a",
		"no_test": true,
	}})
	if err != nil || res.IsError {
		t.Fatalf("partitions no_test: %v %v", err, res)
	}
}

// TestVerifyToolNamesPolicyRecordProblem pins the tree-fact
// classification of a policy record problem on the MCP surface: a verify
// call over a tree with no accepted test policy fails carrying the
// record's path and the loader's guidance — never a bare server failure
// — and its terminal cause is TEST_FAILURE, the same classification the
// unified check gives the condition, so an agent distinguishes no-policy
// from server fault without guessing (REQ-mcp-progress).
func TestVerifyToolNamesPolicyRecordProblem(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	// A real tree with no policy record: the production witnessing seam
	// fails through the one shared loading seam.
	dir := t.TempDir()
	s := &Server{
		fsys: func() fs.FS {
			return fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte(doc)},
			}
		},
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{}}, nil
		},
		runTests: func(ctx context.Context, _ map[gofresh.Subject]bool) (*verify.TestRun, error) {
			return golang.RunWitnesses(ctx, dir)
		},
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(context.Background(), st) }()
	log := &notificationLog{}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			log.add(req.Params)
		},
	})
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })

	params := &mcp.CallToolParams{Name: "verify", Arguments: map[string]any{}}
	params.SetProgressToken("verify-policy-problem")
	res, err := sess.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("verify over a policy-less tree did not fail")
	}
	text := toolText(t, res)
	if !strings.Contains(text, ".stipulator/policy.textproto") ||
		!strings.Contains(text, "no accepted test policy") {
		t.Fatalf("tool error does not carry the record path and loader guidance:\n%s", text)
	}
	// The terminal notification rides the non-blocking sender after the
	// call returns; poll until a cause-carrying event arrives.
	deadline := time.Now().Add(5 * time.Second)
	cause := stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED
	for time.Now().Before(deadline) && cause == stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED {
		_, cause = phasesOf(t, log.snapshot())
		time.Sleep(5 * time.Millisecond)
	}
	if cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE {
		t.Fatalf("terminal cause = %v, want TEST_FAILURE: a record problem is a tree fact, not a server fault", cause)
	}
}

// The explain tool's reason parser and its guided refusals
// (REQ-mcp-explain).
//
//gofresh:pure
func TestExplainToolParsesAndRefuses(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain")
	pkg, sym, ok := culpritFromReason("post-run validation: package graph shares mutated dynamic state: github.com/x/internal/books: github.com/x/internal/books.normativeThresholds registers function values outside the environment-free audit")
	if !ok || pkg != "github.com/x/internal/books" || sym != "normativeThresholds" {
		t.Fatalf("parse = %q %q %v", pkg, sym, ok)
	}
	pkg, sym, ok = culpritFromReason("github.com/x/reg: github.com/x/reg.Registry escapes writable")
	if !ok || pkg != "github.com/x/reg" || sym != "Registry" {
		t.Fatalf("parse = %q %q %v", pkg, sym, ok)
	}
	pkg, sym, ok = culpritFromReason("example.com/u: example.com/u.Ω registers function values outside the environment-free audit")
	if !ok || pkg != "example.com/u" || sym != "Ω" {
		t.Fatalf("unicode identifier parse = %q %q %v", pkg, sym, ok)
	}
	if _, _, ok := culpritFromReason("reaches testing.Run (test runtime execution)"); ok {
		t.Fatal("effect-plane reason parsed a culprit")
	}
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain", Arguments: map[string]any{
		"reason": "reaches testing.Run (test runtime execution)",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("unparseable reason accepted: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "pass package and symbol") {
		t.Fatalf("refusal lacks guidance: %s", text)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain", Arguments: map[string]any{
		"package": "example.com/reg",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("partial input accepted: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "package and symbol travel together") {
		t.Fatalf("partial-input refusal lacks guidance: %s", text)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain", Arguments: map[string]any{
		"symbol": "Registry",
		"reason": "github.com/x/reg: github.com/x/reg.Other escapes writable",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("lone symbol silently discarded for the reason: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "package and symbol travel together") {
		t.Fatalf("lone-symbol refusal lacks guidance: %s", text)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("empty input accepted: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "pass a reason to parse, or package and symbol") {
		t.Fatalf("empty-input refusal lacks guidance: %s", text)
	}
}

// The tool's wire projection is pinned end to end: the explicit
// package-and-symbol arm reaches the backend verbatim, every chain
// link field and the omission count cross into the structured result,
// and the digest names the arm, link count, and answering view - with
// an empty chain stated as such (REQ-mcp-explain,
// REQ-mcp-response-contract).
//
//gofresh:pure
func TestExplainToolProjectsChain(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain")
	var gotPkg, gotSym string
	chain := gofresh.Chain{
		Arm: "environment-audit",
		Links: []gofresh.ChainLink{
			{Kind: "edge", Package: "example.com/reg", Symbol: "Registry", Callee: "gen", Clause: "a binding source refused", Pos: "reg.go:12"},
			{Kind: "refusal", Package: "example.com/reg", Symbol: "gen", Clause: "a stored value refused", Pos: "reg.go:7"},
		},
		Omitted: 3,
	}
	sess, _ := harnessWith(t, nil, func(s *Server) {
		s.explain = func(_ context.Context, pkgPath, symbol string) (gofresh.Chain, string, error) {
			gotPkg, gotSym = pkgPath, symbol
			if symbol == "Missing" {
				return gofresh.Chain{}, "", nil
			}
			return chain, "race", nil
		}
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain", Arguments: map[string]any{
		"package": "example.com/reg", "symbol": "Registry",
	}})
	if err != nil || res.IsError {
		t.Fatalf("explain failed: %v %+v", err, res)
	}
	if gotPkg != "example.com/reg" || gotSym != "Registry" {
		t.Fatalf("explicit culprit not forwarded: %q %q", gotPkg, gotSym)
	}
	var out explainOut
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	want := explainOut{Arm: "environment-audit", View: "race", Omitted: 3, Links: []explainLink{
		{Kind: "edge", Package: "example.com/reg", Symbol: "Registry", Callee: "gen", Clause: "a binding source refused", Pos: "reg.go:12"},
		{Kind: "refusal", Package: "example.com/reg", Symbol: "gen", Clause: "a stored value refused", Pos: "reg.go:7"},
	}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("projection = %+v, want %+v", out, want)
	}
	if text := toolText(t, res); text != "explain: environment-audit, 2 links in the structured result; view: race; 3 omitted" {
		t.Fatalf("digest = %q", text)
	}
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "explain", Arguments: map[string]any{
		"package": "example.com/reg", "symbol": "Missing",
	}})
	if err != nil || res.IsError {
		t.Fatalf("empty-chain call failed: %v %+v", err, res)
	}
	if text := toolText(t, res); !strings.Contains(text, "no chain") {
		t.Fatalf("empty chain not stated in the digest: %q", text)
	}
}

// The gap list leads with its dangling rows and caps with the
// remainder counted: a capped list must drop ordinary evaluated rows
// before the rows demanding repair, and the count line reports the
// uncapped total (REQ-mcp-response-contract).
func TestGapListLeadsWithDanglingAndCapsCounted(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	files := map[string]string{}
	for i := 0; i < 53; i++ {
		files[fmt.Sprintf(".stipulator/gaps/d%02d.textproto", i)] = fmt.Sprintf("requirement_id: \"REQ-dangling-%02d\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n", i)
	}
	files[".stipulator/gaps/e1.textproto"] = "requirement_id: \"REQ-m-a\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n"
	files[".stipulator/gaps/e2.textproto"] = "requirement_id: \"REQ-m-b\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n"
	sess, _ := harness(t, files)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "gap", Arguments: map[string]any{"list": true}})
	if err != nil || res.IsError {
		t.Fatalf("gap list: %v %v", err, res)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "55 gap records") {
		t.Fatalf("count line lost the uncapped total: %s", text)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Gaps        []map[string]any `json:"gaps"`
		GapsOmitted int              `json:"gapsOmitted"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("structured shape: %v: %s", err, b)
	}
	if len(out.Gaps) != 50 || out.GapsOmitted != 5 {
		t.Fatalf("cap = %d rows, %d omitted; want 50 and 5", len(out.Gaps), out.GapsOmitted)
	}
	for i, row := range out.Gaps {
		if state, _ := row["state"].(string); !strings.Contains(state, "DANGLING") {
			t.Fatalf("row %d is %v — dangling rows must lead, so a capped list drops evaluated rows first", i, row["state"])
		}
	}
}
