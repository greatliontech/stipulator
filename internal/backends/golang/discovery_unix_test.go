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

// TestGoDiscoveryCancellationTerminatesDescendants pins the owned process
// boundary of package discovery: a discovery whose go command starts its
// own slow child (a grandchild of stipulator) is cancelled, and the whole
// descendant tree dies with it — package loading owns its launcher's
// descendants exactly as test invocations own theirs.
func TestGoDiscoveryCancellationTerminatesDescendants(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	stipulate.Covers(t, "REQ-policy-cancellation")
	dir := t.TempDir()
	bin := dir + "/bin"
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := dir + "/grandchild.pid"
	// The fake go command spawns a slow grandchild, publishes its pid, and
	// blocks: exactly the shape of a package driver or toolchain subprocess
	// that leaves its own child running.
	script := []byte("#!/bin/sh\n/bin/sleep 300 &\nprintf '%s' \"$!\" > \"$STIPULATOR_TEST_GRANDCHILD\"\nwait\n")
	if err := os.WriteFile(bin+"/go", script, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := &NormalizedInvocation{
		Name:     "cancel",
		Dir:      dir,
		Packages: []string{"./..."},
		Env:      []string{"PATH=" + bin, "STIPULATOR_TEST_GRANDCHILD=" + pidFile},
	}
	done := make(chan error, 1)
	go func() {
		_, err := DiscoverInvocation(ctx, n)
		done <- err
	}()

	var grandchild int
	deadline := time.Now().Add(5 * time.Second)
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
		t.Fatal("discovery's grandchild never started")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled discovery returned %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not stop after cancellation")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(grandchild, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived discovery cancellation", grandchild)
}
