//go:build unix && !linux && !solaris

package golang

import "syscall"

// rlimitUnlimited reports the platform's RLIM_INFINITY — the int64
// maximum on darwin, the BSDs and aix, a constant both field types
// hold.
func rlimitUnlimited[T ~int64 | ~uint64](v T) bool { return v == syscall.RLIM_INFINITY }
