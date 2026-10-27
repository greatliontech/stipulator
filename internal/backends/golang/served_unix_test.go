//go:build unix

package golang

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// The declaration-reading forwarders surface the typed load's fault: a
// preview distinguishes "does not resolve" from "the load failed"
// only through the error, so a broken module errors SymbolFile and
// ReachedPackages rather than answering absence (REQ-go-owned-processes).
func TestWholeTreeForwardersSurfaceLoadFault(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := NewWholeTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	if _, ok, err := whole.SymbolFile("example.com/p.F"); err == nil || ok {
		t.Fatalf("SymbolFile over an unloadable module = %v, %v; want the load fault", ok, err)
	}
	if reach, err := whole.ReachedPackages([]string{"p/p.go"}); err == nil || len(reach) != 0 {
		t.Fatalf("ReachedPackages over an unloadable module = %v, %v; want the load fault", reach, err)
	}
}

// TestGoResolverCancellationTerminatesDescendants pins the owned process
// boundary of symbol loading: a typed load whose go list launcher is
// cancelled mid-load terminates the launcher process — package loading
// owns its launcher's process under the operation's context.
func TestGoResolverCancellationTerminatesDescendants(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes", "REQ-policy-cancellation")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fakebin := dir + "/bin"
	if err := os.Mkdir(fakebin, 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := dir + "/launcher.pid"
	script := []byte("#!/bin/sh\nif [ \"$1\" = env ]; then exec \"" + goBinary + "\" \"$@\"; fi\nprintf '%s' \"$$\" > \"$STIPULATOR_TEST_GRANDCHILD\"\nexec /bin/sleep 300\n")
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
	whole, err := NewWholeTree(ctx, mod)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	done := make(chan error, 1)
	go func() {
		_, _, err := whole.Resolve("example.com/slow.X")
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
		t.Fatal("the go command's grandchild never started")
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

	deadline = time.Now().Add(5 * time.Second)
	grandchildDead := false
	for time.Now().Before(deadline) && !grandchildDead {
		grandchildDead = errors.Is(syscall.Kill(grandchild, 0), syscall.ESRCH)
		if !grandchildDead {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !grandchildDead {
		t.Errorf("grandchild %d survived resolver cancellation", grandchild)
	}
}
