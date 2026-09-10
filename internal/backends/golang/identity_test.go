package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// Two reads of one file agree; a rename-replace (a new inode) and an
// in-place rewrite (the same inode, a new size or modification time)
// each yield a different identity (REQ-go-owned-processes).
func TestFileIdentityMovesWithTheFile(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("build one"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := fileIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := fileIdentity(path); err != nil || again != first {
		t.Fatalf("one file, two identities: %q %q (%v)", first, again, err)
	}
	replacement := filepath.Join(dir, "bin.new")
	if err := os.WriteFile(replacement, []byte("build one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	renamed, err := fileIdentity(path)
	if err != nil || renamed == first {
		t.Fatalf("a rename-replace kept the identity %q (%v)", renamed, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(" and more"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	rewritten, err := fileIdentity(path)
	if err != nil || rewritten == renamed {
		t.Fatalf("an in-place rewrite kept the identity %q (%v)", rewritten, err)
	}
	// An equal-size rewrite on the same inode moves only the
	// modification time — the ordinary rebuilt binary — and that alone
	// must move the identity.
	if err := os.WriteFile(path, []byte("build two and more"), 0o755); err != nil {
		t.Fatal(err)
	}
	if same, err := fileIdentity(path); err != nil || same == rewritten {
		t.Fatalf("an equal-size rewrite kept the identity %q (%v)", same, err)
	}
	if _, err := fileIdentity(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing file has an identity")
	}
}

// The self-executed path meets the image this process started as: a
// parent whose image moved refuses its own re-executed binary — the
// live child answers the file it runs as, the parent expects what it
// sampled at start (REQ-go-owned-processes).
func TestSelfExecutedChildIsRefusedWhenTheParentsImageMoved(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	dir, err := filepath.Abs(filepath.Join("testdata", "fixturemod"))
	if err != nil {
		t.Fatal(err)
	}
	prior, priorErr := selfIdentity, selfIdentityErr
	selfIdentity, selfIdentityErr = "stale-build", nil
	t.Cleanup(func() { selfIdentity, selfIdentityErr = prior, priorErr })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := newResolverClientScoped(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.identity != "stale-build" {
		t.Fatalf("the self-executed client expects %q, want the image sampled at start", c.identity)
	}
	_, _, err = c.Resolve("example.com/fixture/lib.Add")
	if err == nil || !strings.Contains(err.Error(), "executable changed") || !strings.Contains(err.Error(), "stale-build") || !strings.Contains(err.Error(), prior) {
		t.Fatalf("moved image accepted or misnamed: %v", err)
	}
	// The parent that cannot read its own image trusts no child.
	selfIdentity, selfIdentityErr = "", os.ErrNotExist
	blind, err := newResolverClientScoped(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blind.Close()
	if _, _, err := blind.Resolve("example.com/fixture/lib.Add"); err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("a parent with no readable image accepted a child: %v", err)
	}
}

// A self-executed child of the same build whose tree fails to load
// reports that load error as this tree's — the identities agree, so
// the error line is trusted — never a skew refusal
// (REQ-go-owned-processes).
func TestSelfExecutedChildsLoadErrorIsItsOwn(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	missing := filepath.Join(t.TempDir(), "missing")
	c, err := newResolverClientScoped(ctx, missing, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _, err = c.Resolve("example.com/p.F")
	if err == nil || strings.Contains(err.Error(), "executable changed") || strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("a same-build child's load error read as a skew: %v", err)
	}
	if !strings.Contains(err.Error(), "owned resolver child") {
		t.Fatalf("load error lost its provenance: %v", err)
	}
}
