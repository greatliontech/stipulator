//go:build unix

package golang

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// statInfo is a file info whose only fact is its owner.
type statInfo struct {
	uid uint32
	dir bool
}

func (i statInfo) Name() string       { return "root" }
func (i statInfo) Size() int64        { return 0 }
func (i statInfo) Mode() os.FileMode  { return 0o700 }
func (i statInfo) ModTime() time.Time { return time.Time{} }
func (i statInfo) IsDir() bool        { return i.dir }
func (i statInfo) Sys() any           { return &syscall.Stat_t{Uid: i.uid} }

// The owner predicates read the uid off the file's stat: the caller's
// uid is the caller's own, root's is root's, any other is neither, and
// a file info with no stat is owned by nobody (REQ-go-owned-processes).
func TestOwnerPredicatesReadTheUid(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	me := uint32(os.Getuid())
	if !ownedByCaller(statInfo{uid: me, dir: true}) {
		t.Fatal("the caller's own uid was not the caller's own")
	}
	if ownedByCaller(statInfo{uid: me + 1, dir: true}) {
		t.Fatal("another uid passed as the caller's own")
	}
	if os.Getuid() != 0 && ownedByRoot(statInfo{uid: me, dir: true}) {
		t.Fatal("the caller's uid passed as root's")
	}
	if !ownedByRoot(statInfo{uid: 0, dir: true}) {
		t.Fatal("uid 0 was not root's")
	}
	if ownedByCaller(noStat{}) || ownedByRoot(noStat{}) {
		t.Fatal("a file info with no stat was owned by someone")
	}
}

type noStat struct{ statInfo }

func (noStat) Sys() any { return nil }
