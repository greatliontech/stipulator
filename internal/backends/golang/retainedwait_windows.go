package golang

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

const retainedCanObserveExit = true

func retainedAwaitExit(p *retainedCommand) error {
	pid := p.cmd.Process.Pid
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, windows.INFINITE)
	if err != nil {
		return err
	}
	if state != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("waiting for retained process %d: state %d", pid, state)
	}
	return nil
}

func retainedSweep(_ *exec.Cmd, cancel func() error) error {
	// The unreaped exec.Cmd retains the process handle, pinning the ID until
	// this tree-kill helper (the owned runner's Windows mechanism) returns.
	err := cancel()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
