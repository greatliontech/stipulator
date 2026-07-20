package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{
				"example.com/p.TestA": strings.Repeat("s", 64),
				"example.com/p.F":     strings.Repeat("f", 64),
			}}, nil
		},
		runTests: func(context.Context) (*verify.TestRun, error) {
			return &verify.TestRun{
				RaceEnabled: true,
				Outcomes:    map[string]verify.TestOutcome{"example.com/p.TestA": verify.TestPassed},
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
		runTests: func(ctx context.Context) (*verify.TestRun, error) {
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
	var contextDescription, targetsDescription string
	for _, tool := range list.Tools {
		got[tool.Name] = true
		if tool.Name == "context" {
			contextDescription = tool.Description
		}
		if tool.Name == "targets" {
			targetsDescription = tool.Description
		}
	}
	want := []string{"compile", "verify", "gate", "check", "bind", "unbind", "gap", "pin", "prune", "read_spec", "context", "partitions", "dispose", "targets", "attest_requirement"}
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
	if !strings.Contains(targetsDescription, "binding surfaces") || strings.Contains(targetsDescription, "mutation") || strings.Contains(targetsDescription, "reqs") {
		t.Fatalf("targets description is stale: %q", targetsDescription)
	}
}

// TestTargetsToolWiring pins the read-only structured report, valid empty
// corpus, intersecting array filters, and retired input rejection.
//
//gofresh:pure
func TestTargetsToolWiring(t *testing.T) {
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "targets", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	structured, ok := res.StructuredContent.(map[string]any)
	if res.IsError || !ok || structured["format"] != "stipulator.binding-surfaces/v1" {
		t.Fatalf("empty surface report = %+v", res)
	}
	if surfaces, ok := structured["surfaces"].([]any); !ok || len(surfaces) != 0 {
		t.Fatalf("empty surfaces = %#v", structured["surfaces"])
	}

	bindings := pinnedBinding(t) + `bindings {
  requirement_id: "REQ-m-a"
  backend: "go"
  symbol: "example.com/p.F"
  role: BINDING_ROLE_IMPLEMENTS
}
`
	sess, _ = harness(t, map[string]string{".stipulator/bindings/m.textproto": bindings})
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "targets", Arguments: map[string]any{
		"requirements": []string{"REQ-m-a", "REQ-absent"},
		"backends":     []string{"go"},
		"symbols":      []string{"example.com/p.F"},
	}})
	if err != nil || res.IsError {
		t.Fatalf("filtered report = %+v, %v", res, err)
	}
	structured, ok = res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("filtered structured content = %#v", res.StructuredContent)
	}
	surfaces, ok := structured["surfaces"].([]any)
	if !ok || len(surfaces) != 1 {
		t.Fatalf("filtered surfaces = %#v", structured["surfaces"])
	}
	for _, arguments := range []map[string]any{
		{"backends": []string{"absent"}},
		{"requirements": []string{"REQ-m-b"}},
		{"symbols": []string{"example.com/p.Missing"}},
		{"out": "targets.json"},
		{"staged_diff": true},
		{"reqs": "REQ-m-a"},
	} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "targets", Arguments: arguments})
		if err == nil && !res.IsError {
			t.Fatalf("targets accepted invalid or empty selection input %v: %+v", arguments, res)
		}
	}
}

// TestCompileToolCounts pins the compile result's two arms: a clean corpus
// reports the IR counts, an erroring corpus omits them entirely rather than
// reporting a misleading zero — absent means "no IR, not computed".
//
//gofresh:pure
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
}

//gofresh:pure
func TestReadSpecToolMirrorsBundle(t *testing.T) {
	sess, _ := harness(t, nil)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_spec", Arguments: map[string]any{
		"ids": "REQ-m-a",
	}})
	if err != nil || res.IsError {
		t.Fatalf("read_spec: %v %v", err, res)
	}
	// The bundle rides the text content once; the structured result
	// carries only its size (REQ-mcp-response-contract).
	if text := toolText(t, res); !strings.Contains(text, "widget") {
		t.Fatalf("read_spec lacks closure content: %s", text)
	}
}

// TestAttestTools pins the MCP surface of the requirement attest verb:
// writes land in the record store, and refusals surface as tool errors.
//
//gofresh:pure
func TestAttestTools(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-evidence-attestation")
	sess, writes := harness(t, nil)

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "attest_requirement", Arguments: map[string]any{
		"requirement": "REQ-m-a", "reason": "judged by review",
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

	// Reasonless requests surface as tool errors.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "attest_requirement", Arguments: map[string]any{
		"requirement": "REQ-m-b",
	}})
	if err != nil || !res.IsError {
		t.Fatalf("reasonless attestation accepted: %v %v", err, res)
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
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "pin", Arguments: map[string]any{
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

	// Unknown id: quoted cleanly, no mangling.
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"ids": "REQ-m-ghost",
	}})
	if err != nil || !res.IsError {
		t.Fatal("unknown id accepted")
	}
	if msg := fmt.Sprint(res.Content[0]); !strings.Contains(msg, `"REQ-m-ghost" is not in the corpus`) {
		t.Fatalf("unknown id not quoted cleanly: %s", msg)
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
		runTests: func(ctx context.Context) (*verify.TestRun, error) { return golang.RunWitnesses(ctx, dir) },
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
