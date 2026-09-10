package golang

import (
	"fmt"
	"os"
)

// selfIdentity is this process's executable identity, sampled when the
// package initializes — the file the process was started as, before
// anything in this process's life could replace it (a replacement in
// the window between exec and init is the declared residual): a
// rename-replace moves the inode, an in-place rewrite the size or
// modification time, and either turns a later self-executed child into
// a different build. selfIdentityErr is set when the image cannot be
// read; a client of this process then refuses every child, since it
// cannot tell its own build from another.
var selfIdentity, selfIdentityErr = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fileIdentity(exe)
}()

// fileIdentity is the identity of the executable at path — its size,
// modification time, and, where the platform exposes one, its inode —
// read through any symlink, so two spellings of one file agree. Two
// files of equal size and modification time on one inode are one
// identity: an in-place rewrite preserving both is the residual.
func fileIdentity(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%s", fi.Size(), fi.ModTime().UnixNano(), inodeOf(fi)), nil
}
