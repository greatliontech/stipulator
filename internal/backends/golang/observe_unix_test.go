//go:build unix

package golang

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestRootsProbeIsSweptWithTheOperation pins the roots probe's
// containment: a bare `go env -json` forks the configured C compiler,
// and a compiler that hangs — a wrapper on an unreachable host — is a
// descendant the operation's cancellation must sweep, which a plain
// runner in the caller's group would orphan (REQ-go-owned-processes-runner).
// The invocation declares CC as a shim that, once the test raises a
// flag after the capture (the normalization's own snapshot consults
// the compiler too, and must not hang), records its pid and sleeps;
// the operation is cancelled once the shim runs; the shim is gone
// after the run returns. A plain-witness invocation: a race build
// would need the compiler for the detector's runtime.
//
// Deliberately not //gofresh:pure: executes the fixture's tests.
func TestRootsProbeIsSweptWithTheOperation(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes-runner")
	if testing.Short() {
		t.Skip("executes a plain-witness invocation over a temporary module")
	}
	neutralAmbient(t)
	tmp := writeModule(t, map[string]string{
		"go.mod":      "module example.com/swept\n\ngo 1.26\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
	})
	// The shim hangs only for the roots probe — the one go command of
	// the operation that runs IN the package directory (the loader's
	// listings, discovery's and the witness spawn run in the tree root)
	// — so the loader, the clause's declared gap outside any group,
	// never meets it.
	shimDir := t.TempDir()
	pidFile := filepath.Join(shimDir, "cc.pid")
	flag := filepath.Join(shimDir, "hang")
	shim := filepath.Join(shimDir, "cc")
	pkgDir := filepath.Join(tmp, "a")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nif [ -e "+flag+" ] && [ \"$(pwd)\" = \""+pkgDir+"\" ]; then echo $$ > "+pidFile+"; exec sleep 300; fi\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain := &stipulatorv1.GoInvocationConfig{}
	plain.SetPackages([]string{"./a"})
	plain.SetPlainWitness(true)
	plain.SetEnvironment([]string{"CC=" + shim})
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", plain)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc := mustCapture(t, ctx, tmp, pol)
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			if _, err := os.Stat(pidFile); err == nil {
				cancel()
				return
			}
		}
	}()
	_, _, _ = ExecutePolicyWitnessed(ctx, pc, noSeeding{})
	cancel()
	<-done
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the shim never ran: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("the shim's pid: %q", raw)
	}
	deadline := time.Now().Add(15 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the compiler shim (pid %d) survived the operation's cancellation: the roots probe's descendant was not swept", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
