# gofresh corpus pin lags the latest release (fleet sweep 2026-09-28)

The weekly fleet sweep's shape-corpus check reports this repo's gofresh
pin at v0.105.1 against a latest remote tag of v0.107.0: LAG. The pin
is chunk 272's bump (the gotool copies and guidance projections deleted
for gofresh's; behind gofresh 265, 266, 274, and 279). gofresh has
released twice since: the local gofresh clone, last fetched
2026-09-22, holds tags through v0.105.1 and its `origin/main` sits at
the 279 landing, so what v0.106.0 and v0.107.0 carry is not readable
from this machine — the remote is the authority the sweep reads, and
CI mints tags the local clone lags. Corpus content drift is
unrepresentable; version lag is the one drift channel left, and 279
declared that every consumer's machine-local records re-measure once
at the bump that follows it.

Scanned: the index and all 29 docs. witnesscache-fingerprint-mirror
conditions on "the next gofresh fingerprint field addition" and
structural-data-with-methods records the 272 fingerprint change as
history; environment and sampler docs closed at 272. None schedules a
gofresh bump on its own, so this doc mints no second trigger for
scheduled work — no train chunk charters the bump either: gofresh 281's
charter names "the consumers' next bumps" (the containment copies
delete there) without numbering them, and stipulator's queued chunks
(270, 226, 238, 249, 185, 225+248, 228, 184) carry no bump rider.

Lands: cross-tool train — awaiting triage (the stipulator bump behind
gofresh 279 and the remote's v0.107.0, and behind 281 once it releases;
the next chunk-open gate slots it as a numbered chunk or a rider on
one, per the train's cross-session-filing rule).
