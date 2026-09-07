# The toolchain's telemetry sidecar escapes the owned-process boundary

REQ-go-owned-processes requires a witness child's whole descendant
tree to end with its cancellation, and the runner spawns children in
their own process group for that. The toolchain's telemetry, on by
default, forks a detached upload sidecar (setsid) on its once-a-day
upload check — every run, for a fresh config home — so one descendant
leaves the group and outlives
the cancellation by design of the toolchain — it writes under the
user's home, outside the tree and outside every classification root,
so no evidence is affected, but the contract's "whole tree" is not
literally kept.

Resolution: decide whether the witness spawn environment turns
telemetry off for its children (a mode file under a per-run config
home, since no environment variable sets the mode), making the
descendant tree exactly what the runner owns, or the clause states
the toolchain's detached telemetry as the one sanctioned escape.

Lands: cross-tool train chunk 166