# gofresh corpus pin lags the latest release (fleet sweep 2026-10-12)

The weekly fleet sweep's shape-corpus check reports this module pinned
to gofresh v0.115.1 while the latest release tag is v0.116.1 (the
remote is the authority; the local clone may lag it). Version lag is
the one drift channel the shape corpus leaves open — corpus content
drift is unrepresentable — so a consumer behind the release judges
purity, inputs and freshness with an older corpus than the one
released. The remedy is the explicit adoption named below: select the release
carrying the required package-producing form, complete its adoption riders,
and verify the tests. Rebuild from clean committed source, replace both
installed copies, and restart long-lived readers. The sweep's next corpus line
reading `current` is the check that closes this doc.

Scanned: the index and all 33 docs it lists. None schedules a gofresh
bump: the 2026-09-28 pin-lag doc closed at the v0.111.x bump (train
290r) and is gone; seeding-walk-unreached-routes waits on a gofresh
query, not a version. Rows disjoint; no scheduled trigger duplicated.

Lands: tooling-redesign 5.2d, after 5.2c releases the package-producing form.
Adopt complete compilation and runtime descriptions with that native unit;
the earlier selection/certificate work does not make the legacy producer an
adopted native producer. A dependency-only bump that leaves producing paths
degraded is not completion of this obligation.
