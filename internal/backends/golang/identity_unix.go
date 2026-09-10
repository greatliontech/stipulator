//go:build unix

package golang

import (
	"os"
	"strconv"
	"syscall"
)

func inodeOf(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Ino), 10)
	}
	return "-"
}
