# the corpus check is red with residue from the five spec commits after the last pin update

The five spec-editing commits after the last `.stipulator/` update (the
check summary diet, the gap read form, prune's gap-bound narrowing,
check's ids scoping, verify's classification verdicts) left the corpus
in a red state the tree has carried since:

- **Stale content pins awaiting re-consent** across eight requirements
  (`REQ-check-verdict` +20 bindings, `REQ-evidence-freshness-degrade`,
  `REQ-gap-list`, `REQ-gap-resolved-pruned`, `REQ-go-witness-class`,
  `REQ-mcp-response-contract`, `REQ-mcp-views`,
  `REQ-report-check-result`): the clause texts moved, the pins did not.
  Each needs the binding-vs-clause faithfulness walk before
  `stipulator pin --req <id>` re-consents it.
- **Bindings to deleted tests**: the summary diet removed the reason
  histograms, and bindings still name their tests (e.g.
  `internal/views.TestCheckViewHistogramKeyCap` — "produced no outcome").
  Each needs retargeting to the surviving blocker-row tests or
  disposal.
- **Unbacked coverage registrations**: tests added by those commits
  register `Covers()` with no tests-role binding behind them
  (`internal/mcpserver`, `internal/cmd`, `internal/views`,
  `TestGoRunWitnessesScopedDegradeExecutesScopeOnly`). Each needs its
  binding authored.

A scoped check on requirements outside this residue passes; the
whole-tree verdict stays red until the walk lands.

Lands: before the next stipulator release tag (the canonical gate must
be green to cut a release).
