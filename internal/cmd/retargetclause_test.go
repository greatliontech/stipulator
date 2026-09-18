package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// resolvingBackend answers every symbol as resolved with one shape.
type resolvingBackend struct{}

func (resolvingBackend) Resolve(string) (verify.Resolution, string, error) {
	return verify.Resolved, strings.Repeat("r", 64), nil
}

func (resolvingBackend) Close() error { return nil }

// The CLI's retarget preview accepts a store holding two distinct-clause
// claims on one untouched symbol — two claims, never a collision — and
// writes nothing (REQ-change-retarget, REQ-evidence-clause-claim).
//
//gofresh:pure
func TestRetargetCLIPreviewAdmitsDistinctClauseClaims(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retarget", "REQ-evidence-clause-claim")
	priorDir, priorMake := chdir, makeBackends
	chdir = t.TempDir()
	t.Cleanup(func() { chdir, makeBackends = priorDir, priorMake })
	for path, content := range map[string]string{
		".stipulator/manifest.textproto": "include: \"spec.md\"\n",
		"spec.md": "# Fixture\n\n**REQ-fixture-context** (behavior): The implementation MUST support both operations:\n\n" +
			"- **prepared-operations**: Prepare operations.\n- **grant-lifetime**: Bound the grant lifetime.\n\n" +
			"**REQ-fixture-move** (behavior): The implementation MUST execute an operation.\n",
		".stipulator/bindings/fixture.textproto": "" +
			"bindings { requirement_id: \"REQ-fixture-context\" backend: \"go\" symbol: \"example.com/fixture.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared-operations\" }\n" +
			"bindings { requirement_id: \"REQ-fixture-context\" backend: \"go\" symbol: \"example.com/fixture.Claim\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"grant-lifetime\" }\n" +
			"bindings { requirement_id: \"REQ-fixture-move\" backend: \"go\" symbol: \"example.com/fixture.Old\" role: BINDING_ROLE_IMPLEMENTS }\n",
	} {
		full := filepath.Join(chdir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(filepath.Join(chdir, ".stipulator/bindings/fixture.textproto"))
	if err != nil {
		t.Fatal(err)
	}
	makeBackends = func(context.Context, string) (map[string]verify.Backend, func() error, error) {
		return map[string]verify.Backend{"go": resolvingBackend{}}, resolvingBackend{}.Close, nil
	}
	var out bytes.Buffer
	cmd := retargetCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--from", "example.com/fixture.Old", "--to", "example.com/fixture.New", "--check"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("preview refused the store: %v", err)
	}
	if !strings.Contains(out.String(), "REQ-fixture-move  example.com/fixture.Old -> example.com/fixture.New") || !strings.Contains(out.String(), "check only: 1 binding(s) would retarget") {
		t.Fatalf("preview = %q", out.String())
	}
	after, err := os.ReadFile(filepath.Join(chdir, ".stipulator/bindings/fixture.textproto"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the check form wrote: %v", err)
	}
	// A refused preview — a rewrite collapsing a claim onto a standing
	// one of the same clause — writes nothing either.
	if err := os.WriteFile(filepath.Join(chdir, ".stipulator/bindings/collide.textproto"), []byte("bindings { requirement_id: \"REQ-fixture-context\" backend: \"go\" symbol: \"example.com/fixture.Old\" role: BINDING_ROLE_IMPLEMENTS clause_ordinal: 1 }\n"+
		"bindings { requirement_id: \"REQ-fixture-context\" backend: \"go\" symbol: \"example.com/fixture.New\" role: BINDING_ROLE_IMPLEMENTS clause_label: \"prepared-operations\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = retargetCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--from", "example.com/fixture.Old", "--to", "example.com/fixture.New", "--check"})
	if err := cmd.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("colliding preview = %v", err)
	}
	after, err = os.ReadFile(filepath.Join(chdir, ".stipulator/bindings/fixture.textproto"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a refused preview wrote: %v", err)
	}
}
