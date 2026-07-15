//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !hurd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package cmd

import "fmt"

func atomicReplace(_, _ string) error {
	return fmt.Errorf("atomic targets output is unsupported on this platform")
}
