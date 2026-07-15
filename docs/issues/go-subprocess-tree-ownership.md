# Go subprocess tree ownership outside Unix witness runs

Lands: when Go package loading execution is next redesigned, or before descendant-cancellation guarantees are claimed for non-Unix platforms.

## Observed

Witness `go test` invocations on Unix can own a process group and terminate every descendant when their operation is canceled. Two other Go execution paths cannot yet provide the same structural guarantee.

`go/packages` owns the command used by package loading. Its context terminates the immediate package driver, but Stipulator cannot attach the driver's descendants to the process group used by witness commands. A package driver or Go tool subprocess that leaves its own child running can therefore outlive cancellation during backend loading.

On Windows, witness cancellation uses the operating system's `taskkill /T` facility. If tree termination itself fails, the direct-process fallback cannot prove that descendants ended. Other non-Unix platforms have only the standard direct-process cancellation behavior.

## Resolution

Put package loading behind an execution boundary whose complete descendant tree Stipulator owns, without weakening typed package results or permitting an ambient external package driver to shape verification. On supported non-Unix platforms, use a native process container or equivalent primitive that makes descendant lifetime inseparable from the operation and test cancellation with a real spawned child.
