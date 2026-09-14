package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/stipulate"
)

// The CLI's declaration-reading backends are the whole-tree form: their
// construction reads no policy, where the serving form's refuses a
// policy the tree cannot parse at construction
// (REQ-evidence-resolution-freshness).
//
//gofresh:pure
func TestDeclarationReadingBackendsReadNoPolicy(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	dir := t.TempDir()
	policyPath := filepath.Join(dir, filepath.FromSlash(policy.Path))
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("this is not a policy {"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := golang.Backends(ctx, dir, nil); err == nil || !strings.Contains(err.Error(), policy.Path) {
		t.Fatalf("the serving form's construction = %v, want the policy refusal", err)
	}
	_, closeBackends, err := makeBackends(ctx, dir)
	if err != nil {
		t.Fatalf("the declaration-reading backends' construction read the policy: %v", err)
	}
	if err := closeBackends(); err != nil {
		t.Fatalf("closing an unopened whole-tree backend: %v", err)
	}
}
