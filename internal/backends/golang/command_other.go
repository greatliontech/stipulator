//go:build !unix && !windows

package golang

import "os/exec"

func configureCommandCancellation(cmd *exec.Cmd) {}
