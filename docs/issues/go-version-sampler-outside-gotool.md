# The GOVERSION sampler runs beside gotool's, not through it

The toolchain-provenance sampler spawns its own `go env GOVERSION` in
the caller's process group, memoizes per (directory, environment), and
salvages the first line a cleanly exited process wrote when a
wrapper's descendant holds the output pipe past the wait delay — a
witnessed behaviour (the shim pins). `gotool.Runner{Prepare}` carries
the boundary hook the sampler needs, but `Runner.Run` returns no
output beside `exec.ErrWaitDelay`, so `Runner.SampleGoVersion` cannot
salvage that answer and adopting it would refuse a toolchain sample
over a wrapper's housekeeping. The sampler joins gotool at the bump
that consumes the release carrying the salvage-capable form (gofresh
docs/issues/gotool-run-discards-the-answer-a-wait-delay-leaves.md);
the memo stays stipulator's, wrapping `Runner.SampleGoVersion`.

Lands: cross-tool train chunk 272 (the bump behind gofresh 265, which carries the sampler).
