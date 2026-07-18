# Go subprocess tree ownership on non-Unix platforms

Lands: when descendant termination inside the owned Go process boundaries is proven on Windows with a real spawned child (the proof facility — a Windows host — is unavailable on the current development host); before descendant-cancellation guarantees are claimed for any non-Unix platform.

## Observed

Go backend work on Unix — witness `go test` invocations, policy normalization, package discovery, and `go/packages` symbol loading (which runs in a self-exec'd resolver child) — runs its child processes inside owned process groups whose process group terminates with the operation's cancellation, proven by cancellation tests that spawn a real grandchild; a descendant that starts its own session escapes the group and is not covered by that proof.

On Windows, cancellation uses the operating system's `taskkill /T` facility. If tree termination itself fails, the direct-process fallback cannot prove that descendants ended, and no Windows host is available to this development environment to test descendant termination with a real spawned child. Other non-Unix platforms have only the standard direct-process cancellation behavior.

## Resolution

On supported non-Unix platforms, use a native process container or equivalent primitive that makes descendant lifetime inseparable from the operation, and test cancellation with a real spawned child on that platform.
