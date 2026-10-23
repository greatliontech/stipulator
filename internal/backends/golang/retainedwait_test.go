//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || solaris

package golang

import (
	"context"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
)

func TestRetainedExitObserverAcceptsAlreadyExitedChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd, err := (gotool.Runner{Containment: &gotool.Containment{}}).Program(ctx, "", os.Environ(), "/bin/sh", "-c", "exit 7")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p, err := startRetainedCommand(ctx, cmd, "binary", retainedProcessHooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := io.Copy(io.Discard, out); err != nil {
		t.Fatal(err)
	}
	// Establish exit with an uncached observation, then observe again through
	// the owner. This exercises a late observer without assuming that closing
	// stdout, a sleep, or goroutine scheduling proves the child has exited.
	if err := retainedAwaitExit(p); err != nil {
		t.Fatalf("initial exit observation: %v", err)
	}
	if err := p.Exited(); err != nil {
		t.Fatalf("already-exited owned child was refused: %v", err)
	}
	if cmd.ProcessState != nil {
		t.Fatal("observer reaped the child before the forest sweep")
	}
	if err := p.Wait(); err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 || p.ownershipErr != nil {
		t.Fatalf("final reap lost exit 7: wait=%v state=%v ownership=%v", err, cmd.ProcessState, p.ownershipErr)
	}
}

func TestRetainedExitObserverWaitsWithoutReaping(t *testing.T) {
	for _, action := range []string{"exit 7", "kill -TERM $$"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd, err := (gotool.Runner{Containment: &gotool.Containment{}}).Program(ctx, "", os.Environ(), "/bin/sh", "-c", "read token; "+action)
			if err != nil {
				t.Fatal(err)
			}
			input, output, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdin = input
			p, err := startRetainedCommand(ctx, cmd, "binary", retainedProcessHooks{})
			_ = input.Close()
			if err != nil {
				output.Close()
				t.Fatal(err)
			}
			defer func() { _ = output.Close(); cancel(); p.Close() }()
			observed := make(chan error, 1)
			go func() { observed <- p.Exited() }()
			select {
			case err := <-observed:
				t.Fatalf("exit observer returned while the child was still blocked: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if _, err := output.WriteString("go\n"); err != nil {
				t.Fatal(err)
			}
			_ = output.Close()
			select {
			case err := <-observed:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("exit observer did not notice the released child")
			}
			if cmd.ProcessState != nil {
				t.Fatal("exit observation reaped the child")
			}
			err = p.Wait()
			if err == nil || p.ownershipErr != nil || cmd.ProcessState == nil {
				t.Fatalf("lost terminal state: wait=%v ownership=%v", err, p.ownershipErr)
			}
			if action == "exit 7" {
				if cmd.ProcessState.ExitCode() != 7 {
					t.Fatal(cmd.ProcessState)
				}
			} else {
				if status := cmd.ProcessState.Sys().(syscall.WaitStatus); status.Signal() != syscall.SIGTERM {
					t.Fatalf("signal replaced: %v", status)
				}
			}
		})
	}
}
