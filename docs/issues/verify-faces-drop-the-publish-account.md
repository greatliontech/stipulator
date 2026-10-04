# The verify, gate and prune faces drop the serving path's publish account

Found at chunk 307.B1's review (2026-10-04). `check` now reads the
served backend's notices after the close that publishes resolution
records, so a refused or degraded publish is stated in its result. The
other faces that build the serving form — `verify` and `gate` through
`verifyrun.Run` (both paths), and `prune` — close their backends through
`verify.CloseBackends` and never read `Notices()`: a record refused on
its source tiers or a publish degraded by a child fault is silent there,
the class 307.B1 fixed for `check`. The MCP declaration-reading verbs
(bind, pin, retarget, context, partitions) take the whole-tree form,
which publishes nothing, and have nothing to report.

The fix is a field on the verify report carrying the account and its
rendering on both faces — a wire change beside a store fix, so it lands
with the account's per-package shape.

Lands: cross-tool train chunk 307 (B2 — discovery restaged per package;
the publish account rides every face that publishes).
