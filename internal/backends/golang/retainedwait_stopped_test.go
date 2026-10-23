//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris

package golang

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

func TestRetainedExitObserverDoesNotCompleteOnStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd, err := (gotool.Runner{Containment: &gotool.Containment{}}).Program(ctx, "", os.Environ(), "/bin/sh", "-c", "kill -STOP $$; exit 7")
	if err != nil {
		t.Fatal(err)
	}
	p, err := startRetainedCommand(ctx, cmd, "binary", retainedProcessHooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); p.Close() }()
	// Establish the actual stopped state without consuming its notification:
	// an observer that incorrectly requests stopped events must still see it.
	for {
		stopped, err := retainedStoppedWithoutConsuming(cmd.Process.Pid)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			t.Fatalf("observing child stop: %v", err)
		}
		if stopped {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("child did not reach stopped state")
		case <-time.After(time.Millisecond):
		}
	}
	observed := make(chan error, 1)
	go func() { observed <- p.Exited() }()
	select {
	case err := <-observed:
		t.Fatalf("stopped child reported exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("continued child was not observed exiting")
	}
	err = p.Wait()
	if err == nil || p.ownershipErr != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatalf("exit 7 lost after stop: wait=%v state=%v ownership=%v", err, cmd.ProcessState, p.ownershipErr)
	}
}
