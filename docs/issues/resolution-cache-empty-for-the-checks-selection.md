# The resolution cache holds nothing for the check's own selection

Measured 2026-09-21 over this repository's own corpus: three
consecutive `stipulator check --full` runs (two from an empty cache
root, the third warm over the second's store) each reported
`resolution: 0 served from records, 880 resolved typed`, and the
resolution store for the corpus held, after all three, one record for
each of the two small invocations (`race:stipulate`,
`race:stipulate/structural`) and none for the `race` invocation that
resolves the other 878 symbols. The machine's long-lived store shows
the same history: 22 resolution records for this corpus, all under the
`default` selection key (an `explain`/`verify` path), none under
`race`, beside 9,643 witness records.

REQ-evidence-resolution-freshness has the served form publish one
record per resolved symbol when its closing capture equals the opening
one; `Served.publish` (internal/backends/golang/served.go) builds the
batch from `s.opening[key]` and skips a symbol whose opening is absent
or whose closure moved, silently, and `captureUnder` records a
degraded note on a failed capture — the check printed no such note.
Which arm empties the `race` batch (no opening capture taken under that
key, every subject judged moved between opening and closing, or a
refused install) is undetermined: settling it needs an instrumented
run over the corpus (45 minutes each), not a probe.

Consequence: every check resolves the whole corpus typed (discovery
1m53s cold; 7.8s warm on gofresh's memos alone), and the serving path
the clause specifies never serves for the tool's own corpus.

Lands: cross-tool train chunk 226 (the served form's split — the
publish path is the seam the split reshapes; instrument the batch
composition per key there and pin the `race` selection's records).
