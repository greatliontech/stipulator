# `check` exited green while naming a failed witness as uncacheable-blocked

Consumer report (bldc, 2026-08-31): `stipulator check` returned
exit 0 / passed:true while its own output carried
`witnessFailureHeadings: failed: …showcase.TestShowcaseValidates`
and `uncacheableBlockers: no healthy process granted the outcome` —
a genuinely failing golden test (a manifest change moved the showcase
source golden) read as blocked-not-failed, so the canonical verdict
stayed green over a red suite; the reviewer's full `go test ./...`
caught it. The verdict now folds a `healthy` term (check.go
SetPassed) — the owner should confirm whether that closes this exact
shape. Ask: a failed witness execution fails the check, or at minimum
the exit code is non-zero whenever witnessFailureHeadings is
non-empty.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
