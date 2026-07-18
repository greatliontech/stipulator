# Go subprocess tree ownership outside Unix-proven boundaries

Lands: when descendant termination inside the owned Go process boundaries is proven on Windows with a real spawned child (the proof facility — a Windows host — is unavailable on the current development host), and when `go/packages` symbol-resolution loading runs behind an owned boundary; before descendant-cancellation guarantees are claimed for any non-Unix platform.

## Observed

Go policy work on Unix — witness `go test` invocations, and now policy normalization and package discovery — runs its child processes inside owned process groups whose process group terminates with the operation's cancellation, proven by cancellation tests for process-group descendants; a descendant that starts its own session escapes the group and is not covered by that proof that spawn a real grandchild. Two execution paths still lack that proof.

`go/packages` symbol-resolution loading (the backend's type-checked package load) owns the command used by package loading. Its context terminates the immediate package driver, but Stipulator cannot attach the driver's descendants to the process group used by owned commands. A Go tool subprocess that leaves its own child running can therefore outlive cancellation during backend symbol loading. Obligation discovery no longer shares this exposure: it drives `go list` directly through the owned boundary.

On Windows, cancellation uses the operating system's `taskkill /T` facility. If tree termination itself fails, the direct-process fallback cannot prove that descendants ended, and no Windows host is available to this development environment to test descendant termination with a real spawned child. Other non-Unix platforms have only the standard direct-process cancellation behavior.

## Resolution

Put `go/packages` symbol loading behind an execution boundary whose complete descendant tree Stipulator owns, without weakening typed package results or permitting an ambient external package driver to shape verification. On supported non-Unix platforms, use a native process container or equivalent primitive that makes descendant lifetime inseparable from the operation, and test cancellation with a real spawned child on that platform.
