//go:build unix

package golang

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/internal/verify"
)

func TestCommandCancellationTerminatesProcessTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pidFile := t.TempDir() + "/child.pid"
	cmd := commandContext(ctx, "sh", "-c", "sleep 30 & child=$!; printf '%s' \"$child\" > \"$1\"; wait", "sh", pidFile)
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

func TestCanceledFullWitnessDiscardsPartialOutcomes(t *testing.T) {
	dir, env, started := fakeBlockingGo(t)
	ctx, cancel := context.WithCancel(context.Background())
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}}
	done := make(chan error, 1)
	go func() {
		_, err := runMemberTests(ctx, dir, env, tr)
		done <- err
	}()
	waitForFile(t, started)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("runMemberTests error = %v, want context canceled", err)
	}
	if len(tr.Outcomes) != 0 {
		t.Fatalf("canceled run published partial outcomes: %v", tr.Outcomes)
	}
}

func TestCanceledSelectiveWitnessDiscardsPartialOutcomes(t *testing.T) {
	dir, env, started := fakeBlockingGo(t)
	ctx, cancel := context.WithCancel(context.Background())
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}}
	run := &selectedRun{outcomes: map[string]string{}, regs: map[string][]verify.Registration{}, capture: map[string]manifestCapture{}}
	done := make(chan error, 1)
	go func() {
		_, err := runOnce(ctx, dir, env, "example.com/p", []string{"TestA"}, tr, run)
		done <- err
	}()
	waitForFile(t, started)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("runOnce error = %v, want context canceled", err)
	}
	if len(tr.Outcomes) != 0 || len(run.outcomes) != 0 {
		t.Fatalf("canceled run published partial outcomes: test=%v selected=%v", tr.Outcomes, run.outcomes)
	}
}

func fakeBlockingGo(t *testing.T) (string, []string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := dir + "/bin"
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	started := dir + "/started"
	script := []byte("#!/bin/sh\nprintf '%s\\n' '{\"Action\":\"pass\",\"Package\":\"example.com/p\",\"Test\":\"TestA\"}'\nprintf started > \"$STIPULATOR_TEST_STARTED\"\n/bin/sleep 30\n")
	if err := os.WriteFile(bin+"/go", script, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin, "STIPULATOR_TEST_STARTED=" + started}
	t.Setenv("PATH", bin)
	return dir, env, started
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, []byte("started")) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
