package golang

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"golang.org/x/sys/unix"
)

func TestRetainedDarwinObserverAttachesAfterZombie(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd, err := (gotool.Runner{Containment: &gotool.Containment{}}).Program(ctx, "", os.Environ(), "/bin/sh", "-c", "exit 7")
	if err != nil {
		t.Fatal(err)
	}
	p, err := startRetainedCommand(ctx, cmd, "binary", retainedProcessHooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Independently establish the kernel's unreaped-child state BEFORE the
	// first invocation of the production observer. Pipe EOF is not an exit
	// barrier, nor is waiting a fixed amount of time after Start.
	for {
		info, err := unix.SysctlKinfoProc("kern.proc.pid", cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if info.Proc.P_pid != int32(cmd.Process.Pid) || info.Eproc.Ppid != int32(os.Getpid()) {
			t.Fatalf("owned child identity changed: %+v", info)
		}
		if info.Proc.P_stat == 5 { // SZOMB, bsd/sys/proc.h
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("child never reached SZOMB")
		case <-time.After(time.Millisecond):
		}
	}
	if err := p.Exited(); err != nil {
		t.Fatalf("late observation of zombie: %v", err)
	}
	if cmd.ProcessState != nil {
		t.Fatal("observer reaped the child")
	}
	err = p.Wait()
	if err == nil || p.ownershipErr != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatalf("exit 7 lost: wait=%v state=%v ownership=%v", err, cmd.ProcessState, p.ownershipErr)
	}
}
