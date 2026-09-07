# Human-facing color is one switch over two streams

`internal/cmd/style.go` decides color once, at package init, requiring
both stdout and stderr to be terminals (NO_COLOR and TERM=dumb off):
a redirected stream must never receive escapes, and the verbs tint
both streams through the same helpers (`red`, `dim`, `green`, …) with
no knowledge of which stream a call renders to. The cost is a real
loss: `stipulator gate 2>/dev/null` in a terminal prints `gate: pass`
uncolored because stderr is not a terminal.

Resolution: a per-stream styler — one value per stream carrying its own
terminal verdict (`out.dim(...)`, `errs.red(...)`) — and every tinting
call site named by the stream it writes to; the global switch and the
stream-blind helpers are deleted.

Lands: cross-tool train chunk 172