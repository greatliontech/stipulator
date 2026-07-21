package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// symlinkTree builds a workspace whose member list includes a lexically
// in-tree path that is a symlink to a directory outside the tree, plus an
// in-tree symlink resolving inside it.
func symlinkTree(t *testing.T, member string) string {
	t.Helper()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module example.com/outside\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/tree\n\ngo 1.22\n",
		"inner/go.mod": "module example.com/tree/inner\n\ngo 1.22\n",
		"go.work":      "go 1.22\n\nuse (\n\t.\n\t./" + member + "\n)\n",
	}
	for name, content := range files {
		p := filepath.Join(tree, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(tree, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree, "inner"), filepath.Join(tree, "inlink")); err != nil {
		t.Fatal(err)
	}
	return tree
}

// A lexically in-tree go.work member that is a symlink out of the tree is
// refused at member enumeration, policy derivation, and invocation
// normalization alike: the escape refusal binds the RESOLVED location,
// not the committed spelling (REQ-go-workspace).
func TestGoSymlinkedMemberEscapeRefused(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace")
	tree := symlinkTree(t, "linked")

	if _, err := workspaceMembers(tree); err == nil || !strings.Contains(err.Error(), "outside the verification tree") {
		t.Errorf("workspaceMembers: err = %v, want the resolved escape refused", err)
	}
	if _, err := policyMembers(tree); err == nil || !strings.Contains(err.Error(), "outside the verification tree") {
		t.Errorf("policyMembers: err = %v, want the resolved escape refused", err)
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetModuleRoot("linked")
	cfg.SetPackages([]string{"./..."})
	if _, err := NormalizeInvocation(context.Background(), tree, goInvocation("linked", cfg)); err == nil || !strings.Contains(err.Error(), "outside the verification tree") {
		t.Errorf("NormalizeInvocation: err = %v, want the resolved escape refused", err)
	}

	// A sibling directory whose path merely string-prefixes the tree
	// root is still outside it: the boundary is a path element, not a
	// character run.
	sibling := tree + "x"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tree, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(tree, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceMembers(tree); err == nil || !strings.Contains(err.Error(), "outside the verification tree") {
		t.Errorf("workspaceMembers(sibling prefix): err = %v, want the resolved escape refused", err)
	}
}

// An in-tree symlink resolving inside the tree stays accepted: the
// refusal binds escapes, not symlinks per se.
func TestGoSymlinkedMemberInsideTreeAccepted(t *testing.T) {
	stipulate.Covers(t, "REQ-go-workspace")
	tree := symlinkTree(t, "inlink")

	members, err := workspaceMembers(tree)
	if err != nil {
		t.Fatalf("workspaceMembers: %v", err)
	}
	if len(members) != 2 || members[1] != "inlink" {
		t.Fatalf("members = %v, want the in-tree symlink kept", members)
	}
	if _, err := policyMembers(tree); err != nil {
		t.Fatalf("policyMembers: %v", err)
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetModuleRoot("inlink")
	cfg.SetPackages([]string{"./..."})
	if _, err := NormalizeInvocation(context.Background(), tree, goInvocation("inlink", cfg)); err != nil {
		t.Fatalf("NormalizeInvocation: %v", err)
	}
}
