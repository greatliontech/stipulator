package golang

import "sync/atomic"

// CountChildSpawnsForTest installs the spawn seam for the test's life and
// returns a reader of the owned resolver children spawned since, for an external
// test package driving a whole pass over this backend. It counts the resolver
// route, excluding retained test binaries. The returned release restores the seam.
func CountChildSpawnsForTest() (children func() int, release func()) {
	var n atomic.Int64
	prior := commandHook
	commandHook = func(name string, args []string) {
		if isResolverSpawn(name, args) {
			n.Add(1)
		}
	}
	return func() int { return int(n.Load()) }, func() { commandHook = prior }
}

// isResolverSpawn classifies the backend's resolver route for both test counters.
func isResolverSpawn(name string, args []string) bool {
	return name != "go" && len(args) > 0 && args[0] == resolverSubcommand
}
