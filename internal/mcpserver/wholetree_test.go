package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// Every declaration-reading tool — bind, pin in both forms, retarget,
// the context slice, partitions — reads its declarations through the
// whole-tree form, never the serving form a verification runs over:
// the serving form reads the policy at construction and publishes
// what it resolves, neither of which a declaration read may do
// (REQ-evidence-resolution-freshness).
//
//gofresh:pure
func TestDeclarationReadingToolsTakeTheWholeTreeForm(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	var serving, whole int
	sess, _ := harnessWith(t, map[string]string{
		".stipulator/bindings/m.textproto": pinnedBinding(t),
	}, func(s *Server) {
		backends := s.backends
		s.backends = func(ctx context.Context, symbols []string) (map[string]verify.Backend, error) {
			serving++
			return backends(ctx, symbols)
		}
		// The harness's whole-tree seam reads the server's backends as
		// they stand, so the count's arm reads the unwrapped fakes.
		s.wholeTree = func(ctx context.Context) (map[string]verify.Backend, error) {
			whole++
			return backends(ctx, nil)
		}
	})
	call := func(name string, args map[string]any) {
		t.Helper()
		if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// The pure declaration readers never build the serving form.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"bind", map[string]any{"claims": []map[string]any{{"requirement": "REQ-m-b", "symbol": "example.com/p.F", "role": "implements"}}}},
		{"pin", map[string]any{}},
		{"pin", map[string]any{"ids": "REQ-m-a"}},
		{"retarget", map[string]any{"from": "example.com/p", "to": "example.com/z", "check": true}},
	} {
		serving, whole = 0, 0
		call(tc.name, tc.args)
		if whole != 1 || serving != 0 {
			t.Fatalf("%s %v built the whole-tree form %d times and the serving form %d times; want 1 and 0", tc.name, tc.args, whole, serving)
		}
	}
	// The verifying readers verify through the serving form and read
	// their declarations through the whole-tree form.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"context", map[string]any{"ids": "REQ-m-a", "no_test": true, "slice": true}},
		{"partitions", map[string]any{"ids": "REQ-m-a", "no_test": true}},
	} {
		serving, whole = 0, 0
		call(tc.name, tc.args)
		if whole != 1 {
			t.Fatalf("%s built the whole-tree form %d times; want 1 (the serving form %d times)", tc.name, whole, serving)
		}
	}
}

// The server's whole-tree seam is the whole-tree form: its
// construction reads no policy, where the serving form's refuses a
// policy the tree cannot parse at construction
// (REQ-evidence-resolution-freshness).
//
//gofresh:pure
func TestServerWholeTreeSeamReadsNoPolicy(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	dir := t.TempDir()
	policyPath := filepath.Join(dir, filepath.FromSlash(policy.Path))
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("this is not a policy {"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	ctx := context.Background()
	if _, err := s.backends(ctx, nil); err == nil || !strings.Contains(err.Error(), policy.Path) {
		t.Fatalf("the serving form's construction = %v, want the policy refusal", err)
	}
	backends, err := s.wholeTree(ctx)
	if err != nil {
		t.Fatalf("the whole-tree form's construction read the policy: %v", err)
	}
	verify.CloseBackends(backends)
}
