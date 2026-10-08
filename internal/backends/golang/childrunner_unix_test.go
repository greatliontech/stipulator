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

// The resolver child's runner is the consumer-command form of the one
// go-command policy: the child starts in its own process group, and
// the operation's cancellation sweeps the group whole — a descendant
// the child left running dies with it (REQ-go-owned-processes).
func TestChildRunnerSweepsTheChildsProcessGroup(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	ctx, cancel := context.WithCancel(context.Background())
	pidFile := t.TempDir() + "/child.pid"
	var seen []string
	prior := commandHook
	commandHook = func(name string, args []string) { seen = append(seen, name+" "+strings.Join(args, " ")) }
	t.Cleanup(func() { commandHook = prior })
	cmd, err := childRunner.Program(ctx, "", os.Environ(), "sh", "-c", "sleep 30 & child=$!; printf '%s' \"$child\" > \"$1\"; wait", "sh", pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "sh -c sleep 30") {
		t.Fatalf("the derivation seam saw %q, want the child's program and arguments once", seen)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("the child is not placed in its own process group: %+v", cmd.SysProcAttr)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatalf("parse child pid: %v", err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child process did not start")
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled command succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command did not stop after cancellation")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d survived cancellation", childPID)
}

// The parent's own environment reaches the resolver child under the
// policy's normalization: an entry the policy refuses — one no Setenv
// can produce, so planted through the seam — refuses the spawn as the
// client's sticky fault naming the entry and never the command line
// (a scoped client's names every package of the stale remainder);
// nothing starts (REQ-go-owned-processes).
func TestResolverChildRefusesAMalformedParentEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	prior := ambientEnviron
	ambientEnviron = func() []string { return []string{"PATH=" + os.Getenv("PATH"), "no-equals-sign"} }
	t.Cleanup(func() { ambientEnviron = prior })
	var spawned int
	priorHook := commandHook
	commandHook = func(string, []string) { spawned++ }
	t.Cleanup(func() { commandHook = priorHook })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newResolverClientCommand(ctx, "/bin/sh", "-c", `printf '{"ready":true}\n'; cat >/dev/null`)
	defer c.Close()
	_, _, err := c.Resolve("example.com/p.F")
	if err == nil || !strings.Contains(err.Error(), "preparing the resolver child: the parent's environment: environment entry 1 is malformed") || strings.Contains(err.Error(), "/bin/sh -c") {
		t.Fatalf("a malformed parent environment = %v, want the spawn refused at its preparation naming the entry, never the command line", err)
	}
	if spawned != 0 {
		t.Fatalf("the seam saw %d spawn(s); want none — the refusal precedes the preparation's seam", spawned)
	}
	if _, _, again := c.Resolve("example.com/p.G"); again == nil || again.Error() != err.Error() {
		t.Fatalf("second question = %v, want the sticky fault %v", again, err)
	}
}
