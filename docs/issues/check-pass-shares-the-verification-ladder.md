# The check pass runs the verification ladder a second time

The verification pass — capture the accepted policy, derive the
operation's symbols, build the served backend set, run the witnesses,
verify — is one core both faces read (internal/verifyrun). The check
pass (internal/check, the ladder at check.go:143-168) runs the same
capture → symbols → served-backend ladder in its own words, differing
in what follows it (the scoped witness-evidence class, suite health,
the check result message). A collapse folds check's ladder onto the
core's — the pass returning the prepared corpus, the report, and the
run, check consuming them — so a refusal decided before a child fires
identically for every judging verb. Invariants preserved: check's
scoped evidence class and its result message; the served backend
opened once per pass.

Lands: user decision — check's pass carries its own scoped-evidence
semantics, so the fold is a design the user schedules.
