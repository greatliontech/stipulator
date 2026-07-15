//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package cmd

import "os"

func atomicReplace(source, destination string) error {
	return os.Rename(source, destination)
}
