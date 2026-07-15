//go:build windows

package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func configureCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		// taskkill /T terminates descendants as well as the go command. Use the
		// system binary under a bound so cancellation cannot hang behind PATH
		// lookup or a wedged tree-termination request.
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := exec.CommandContext(ctx, filepath.Join(root, "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		if err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
