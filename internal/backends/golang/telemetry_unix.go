//go:build unix

package golang

import (
	"os"
	"syscall"
)

// ownedByCaller reports whether info's file is owned by the calling
// user — the fact that makes a directory under a world-writable parent
// this user's own.
func ownedByCaller(info os.FileInfo) bool {
	uid, ok := fileOwner(info)
	return ok && uid == os.Getuid()
}

// ownedByRoot reports a file of the superuser's own — a parent nobody
// but root renames under.
func ownedByRoot(info os.FileInfo) bool {
	uid, ok := fileOwner(info)
	return ok && uid == 0
}

func fileOwner(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
