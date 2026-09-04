//go:build !unix

package main

import (
	"os"
	"syscall"
)

// exitBySignal ends the process with the conventional status for the
// signal that interrupted it; without a re-raisable disposition, the
// code is the account.
func exitBySignal(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok {
		os.Exit(128 + int(s))
	}
	os.Exit(130)
}
