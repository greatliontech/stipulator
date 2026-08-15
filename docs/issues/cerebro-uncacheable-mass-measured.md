# Measured: the cerebro check's 22 minutes are cache misses, not oracle time

A cerebro `check --json` (2026-08-04) reports 2,407 uncacheable witnesses —
near-total cache miss, so every check re-executes the suite under observation
instrumentation. The reason classes: 709 bracket-miss on `go.mod` (repo-root
discovery walks), 533 on `cmd` + 18 on a spec file (tree-scanning oracles), 182
on `.claude` (a foreign session dir inside the repo), 647 caller-supplied-
dynamic refusals, 308 fmt-taint refusals, 8 net/url, 2 startup-effect. The
plain oracle sweep is ~3-5 minutes; the check's ~22 minutes are this mass times
the per-target view multiplication already measured at check-view-cardinality
(24 passes, 86% of the warm floor). The fixes are gofresh's —
bracket-declared-static-inputs and purity-bars-dynamic-and-fmt in gofresh
docs/issues — plus the shared-view fix here; this record is the consumer-side
measurement they land against.

Lands: cross-tool train chunk 16 (the re-measure; delete on that
measurement).
