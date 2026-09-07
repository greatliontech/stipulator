# gofresh pin lags the latest release: v0.95.0 pinned, v0.97.0 tagged (fleet sweep 2026-09-07)

The weekly fleet sweep's shape-corpus check found this repo's `go.mod`
pinning `github.com/greatliontech/gofresh v0.95.0` while the remote
tags v0.97.0. The two releases behind are both the engine side of
train chunk 138: v0.96.0 anchors runtime-input revalidation at an
evidence root above the module and lets an absolute directory bracket
root walk its tree; v0.97.0 adds `Bracket.Reason`, a producer's
preflight of its declared roots. gomutant consumed both; stipulator
and pew did not.

Corpus content drift is unrepresentable — the corpus rides the module
version — so version lag is the one drift channel the sweep can see.
Until the bump, stipulator's witness runs judge bracket roots under the
v0.95.0 engine: an in-tree bracket path spelled relatively, or an
absolute directory declared as a bracket, gets the pre-138 treatment
here while gomutant's judgment of the same policy record differs. The
bump is the whole remedy; the train's release-then-bump doctrine has
the consumer bump ride the consumer's next change set.

Lands: cross-tool train chunk 131 (stipulator's next chunk in the
register's execution order; the bump rides its change set).
