package golang

import "sync/atomic"

// CountChildSpawnsForTest installs the spawn seam for the test's life and
// returns a reader of the owned resolver children spawned since — the
// one seam spawn that is not the go tool — for an external test package
// driving a whole pass over this backend. The returned release restores
// the seam.
func CountChildSpawnsForTest() (children func() int, release func()) {
	var n atomic.Int64
	prior := commandHook
	commandHook = func(name string, args []string) {
		if name != "go" {
			n.Add(1)
		}
	}
	return func() int { return int(n.Load()) }, func() { commandHook = prior }
}
