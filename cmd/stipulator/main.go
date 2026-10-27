// Command stipulator compiles and verifies a specification corpus.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/greatliontech/stipulator/internal/cmd"
)

func main() {
	ctx, stop := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	var received atomic.Value
	go func() {
		if sig, ok := <-signals; ok {
			received.Store(sig)
			// The first signal cancels the run so its ending renders;
			// the default disposition returns at once, so a second
			// signal — the operator's escalation — ends the process
			// outright instead of vanishing into a buffer nobody reads.
			signal.Stop(signals)
			stop()
		}
	}()
	err := cmd.Execute(ctx)
	signal.Stop(signals)
	stop()
	var status cmd.ExitStatus
	var interrupted cmd.Interrupted
	switch {
	case err == nil:
	case errors.As(err, &status):
		// A verdict already rendered: the code alone.
		os.Exit(status.Code)
	case errors.As(err, &interrupted):
		// The ending is rendered; the process now ends as what ended
		// it — the signal itself, re-raised, so a shell loop, make, or
		// a supervisor sees a signal death and not a code. Only a
		// signal cancels this context, so a received one is always
		// here; an interruption without one is an operational fault.
		if sig, ok := received.Load().(os.Signal); ok {
			exitBySignal(sig)
		}
		fmt.Fprintln(os.Stderr, "stipulator:", err)
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "stipulator:", err)
		os.Exit(2)
	}
}
