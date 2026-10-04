//go:build !linux

package resident

// sample answers nothing: the reading has a Linux /proc form only, and
// an absent datum is the honest answer elsewhere.
func sample() (Set, bool) { return Set{}, false }
