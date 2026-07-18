//go:build unix

package golang

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoResolverCancellationTerminatesDescendants pins the owned process
// boundary of symbol loading: a resolver child whose go command starts
// its own slow child (a grandchild of stipulator's grandchild) is
// cancelled mid-load, and the whole descendant tree dies with it —
// package loading owns its launcher's descendants exactly as test
// invocations own theirs.
func TestGoResolverCancellationTerminatesDescendants(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	stipulate.Covers(t, "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("builds the CLI and spawns a resolver child")
	}
	// Build with the real toolchain before the PATH is faked.
	bin := buildResolverCLI(t)
	dir := t.TempDir()
	fakebin := dir + "/bin"
	if err := os.Mkdir(fakebin, 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := dir + "/grandchild.pid"
	// The fake go command spawns a slow grandchild, publishes its pid, and
	// blocks: exactly the shape of a toolchain subprocess that leaves its
	// own child running while the resolver child's load is in flight.
	script := []byte("#!/bin/sh\n/bin/sleep 300 &\nprintf '%s' \"$!\" > \"$STIPULATOR_TEST_GRANDCHILD\"\nwait\n")
	if err := os.WriteFile(fakebin+"/go", script, 0o755); err != nil {
		t.Fatal(err)
	}
	mod := dir + "/mod"
	if err := os.Mkdir(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mod+"/go.mod", []byte("module example.com/slow\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakebin)
	t.Setenv("STIPULATOR_TEST_GRANDCHILD", pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owned := NewOwnedCommand(ctx, bin, ResolverSubcommand, mod)
	defer owned.Close()
	done := make(chan error, 1)
	go func() {
		_, _, err := owned.Resolve("example.com/slow.X")
		done <- err
	}()

	var grandchild int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pidFile)
		if err == nil && len(b) > 0 {
			grandchild, err = strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatalf("parse grandchild pid: %v", err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if grandchild == 0 {
		t.Fatal("the resolver child's grandchild never started")
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled resolution returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolution did not stop after cancellation")
	}

	// Resolve has returned, so the round-trip's hold on the client mutex is
	// over and the child's recorded pid is readable without racing it.
	owned.mu.Lock()
	child := 0
	if owned.cmd != nil && owned.cmd.Process != nil {
		child = owned.cmd.Process.Pid
	}
	owned.mu.Unlock()
	if child == 0 {
		t.Fatal("resolver child pid unavailable after spawn")
	}

	deadline = time.Now().Add(5 * time.Second)
	grandchildDead, childDead := false, false
	for time.Now().Before(deadline) && !(grandchildDead && childDead) {
		if !grandchildDead {
			grandchildDead = errors.Is(syscall.Kill(grandchild, 0), syscall.ESRCH)
		}
		if !childDead {
			childDead = errors.Is(syscall.Kill(child, 0), syscall.ESRCH)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !grandchildDead {
		t.Errorf("grandchild %d survived resolver cancellation", grandchild)
	}
	if !childDead {
		t.Errorf("resolver child %d survived cancellation", child)
	}
}

// TestOwnedResolverChildCrashErrors pins the crash path: a child that
// dies before or after its handshake yields prompt errors from the
// client — a verification error, never a hang and never a silent
// degradation to unowned in-process loading.
func TestOwnedResolverChildCrashErrors(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	t.Run("before handshake", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		owned := NewOwnedCommand(ctx, "/bin/sh", "-c", "exit 1")
		defer owned.Close()
		_, _, err := owned.Resolve("example.com/x.Y")
		if err == nil || !strings.Contains(err.Error(), "owned resolver child") {
			t.Fatalf("crashed child yielded %v, want an owned-resolver error", err)
		}
	})
	t.Run("after handshake", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		owned := NewOwnedCommand(ctx, "/bin/sh", "-c", `printf '{"ready":true}\n'`)
		defer owned.Close()
		_, _, err := owned.Resolve("example.com/x.Y")
		if err == nil || !strings.Contains(err.Error(), "owned resolver child") {
			t.Fatalf("child crashing mid-session yielded %v, want an owned-resolver error", err)
		}
		if _, _, again := owned.Resolve("example.com/x.Y"); again == nil {
			t.Fatal("crash fault was not sticky")
		}
	})
}
