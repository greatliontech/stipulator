//go:build !unix

package golang

import "os/exec"

func retainedQuit(*exec.Cmd) bool { return false }
