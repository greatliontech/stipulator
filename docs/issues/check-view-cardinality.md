# unmeasured: does the check path share the per-target view multiplication?

A measured gomutant campaign (cerebro repro, 2026-08-02) spent ~44% of
warm wall-clock in 273 gofresh observation passes for 54 targets —
caller-side view cardinality, filed in gomutant as
observation-pass-cardinality. Stipulator's check path also drives
gofresh views; whether it batches subjects into shared views or
multiplies passes per target has never been measured (warm checks are
~27s on the gofresh corpus, so the absolute stakes are smaller). One
instrumented check with gofresh's progress events counted per phase
answers it.

Lands: alongside the gomutant observation-pass-cardinality item — one
measurement discipline, both callers.
