//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package recordstore

import (
	"errors"
	"os"
)

func tryLockFile(*os.File) error {
	return errors.New("recordstore: cross-process locking unavailable on this platform")
}
