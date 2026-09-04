//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// exitBySignal ends the process by the signal that interrupted it: the
// default disposition restored and the signal re-raised, so the parent
// observes a signal death, not an exit code that merely names one. The
// raise lands asynchronously — the runtime's handler dies on its own
// thread — so the exit below is the fallback should it not terminate
// the process within its window.
func exitBySignal(sig os.Signal) {
	if s, ok := sig.(syscall.Signal); ok {
		signal.Reset(s)
		_ = syscall.Kill(syscall.Getpid(), s)
		time.Sleep(2 * time.Second)
		os.Exit(128 + int(s))
	}
	os.Exit(130)
}
