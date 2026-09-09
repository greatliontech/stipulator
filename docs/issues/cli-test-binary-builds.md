# Seven test files each build the CLI binary

`internal/cmd`'s CLI-driving tests (check, pin, bind, prune, gap
lifecycle, owned, interruption) each run `go build` for the binary in
their own temp directory — seven builds per package run.

Resolution: one `buildCLI(t)` helper backed by `sync.Once` per test
binary, every CLI-driving test calling it.

Lands: cross-tool train chunk 228