package golang

import "sync/atomic"

// CountChildSpawnsForTest installs the typed-load seam for the test's life and
// returns a reader of the typed loads opened since, for an external test
// package driving a whole pass over this backend. The returned release
// restores the seam.
func CountChildSpawnsForTest() (children func() int, release func()) {
	var n atomic.Int64
	typedLoadHookMu.Lock()
	prior := typedLoadHook
	typedLoadHook = func() {
		n.Add(1)
	}
	typedLoadHookMu.Unlock()
	return func() int { return int(n.Load()) }, func() {
		typedLoadHookMu.Lock()
		typedLoadHook = prior
		typedLoadHookMu.Unlock()
	}
}
