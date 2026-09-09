//go:build !unix

package golang

import "os"

// No owner to read off unix; the seam these guard exists only where a
// variable selects the config home, so they are never consulted here.
func ownedByCaller(os.FileInfo) bool { return false }
func ownedByRoot(os.FileInfo) bool   { return false }
