//go:build (!unix && !windows) || aix

package golang

import (
	"os/exec"
)

// These hosts use the runner's ordinary streamed-command lifecycle: drain
// before Wait, with no subsequent numeric-group signal. They cannot observe
// the leader's exit separately from reaping it.
const retainedCanObserveExit = false

func retainedAwaitExit(*retainedCommand) error    { return nil }
func retainedSweep(*exec.Cmd, func() error) error { return nil }
