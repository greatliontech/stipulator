//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package recordstore

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return errLockContended
		}
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
