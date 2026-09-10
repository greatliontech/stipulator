//go:build unix

package golang

import (
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// shIdentity is the seam's identity for a /bin/sh child.
func shIdentity(t *testing.T) string {
	t.Helper()
	id, err := fileIdentity("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A child that is not the file the client chose to spawn — a binary
// replaced on disk under a running parent — is refused at the
// handshake, the fault naming both identities; nothing is decoded from
// a build this one never wrote (REQ-go-owned-processes).
func TestResolverClientRefusesAChildOfAnotherBuild(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The seam's identity is the spawned file's (sh); the child answers
	// as another build.
	c := newResolverClientCommand(ctx, "/bin/sh", "-c", `printf '{"ready":true,"identity":"other-build"}\n'; cat >/dev/null`)
	defer c.Close()
	_, _, err := c.Resolve("example.com/p.F")
	if err == nil || !strings.Contains(err.Error(), "executable changed") || !strings.Contains(err.Error(), "other-build") || !strings.Contains(err.Error(), c.identity) {
		t.Fatalf("foreign child accepted or misnamed: %v", err)
	}
	// The fault is sticky: a second question meets it without a spawn.
	if _, _, again := c.Resolve("example.com/p.G"); again == nil || again.Error() != err.Error() {
		t.Fatalf("second question = %v, want the sticky fault %v", again, err)
	}
	// An older build answers no identity at all: refused the same way,
	// the message saying so.
	none := newResolverClientCommand(ctx, "/bin/sh", "-c", `printf '{"ready":true}\n'; cat >/dev/null`)
	defer none.Close()
	if _, _, err := none.Resolve("example.com/p.F"); err == nil || !strings.Contains(err.Error(), "sent no identity") {
		t.Fatalf("identity-less child accepted or misnamed: %v", err)
	}
	// A child whose load fails is judged on identity first: a foreign
	// build's load error never reads as this tree's.
	failing := newResolverClientCommand(ctx, "/bin/sh", "-c", `printf '{"error":"foreign load error","identity":"other-build"}\n'`)
	defer failing.Close()
	if _, _, err := failing.Resolve("example.com/p.F"); err == nil || !strings.Contains(err.Error(), "executable changed") || strings.Contains(err.Error(), "foreign load error") {
		t.Fatalf("foreign build's load error accepted as this tree's: %v", err)
	}
}
