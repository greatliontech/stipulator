# The resolution account's "skipped N without a closing capture" names no symbol

Found at chunk 307's clean measurement over gofresh (2026-10-05): every
check reads `resolution: 1017 served from records, 2 resolved typed` and
`resolution published under "default": 0 records; skipped 2 without a
closing capture`. The two symbols resolve typed on every run and never
publish, so they never serve; the account counts them and nothing names
them — the per-symbol typed lines (`resolution typed: <symbol>: <why>`)
exist only for symbols that HOLD a record and failed to serve, and these
hold none. An operator cannot tell which bindings pay the typed
resolution forever or why their closing capture is absent (a subject
gofresh cannot fingerprint under the selection; a symbol outside the
engine's view; a vanished declaration).

The fix is in the account: the skipped and unopened classes name their
symbols, bounded like the refused list (the first eight, then the
remainder counted), on the per-symbol typed channel the CLI and the
full verify report already carry.

Lands: cross-tool train chunk 307 (D — the pass's accumulations; the
account's own residue beside them).
