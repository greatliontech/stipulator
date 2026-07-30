# A witness-policy mismatch floods per-requirement reds instead of one policy diagnostic

Lands: when a check whose witness-eligible selection covers no expected witness
answers with a policy-level verdict arm, and when witness evidence carries its
invocation class so a race-incapable repository can reach a tiered, informative
verdict instead of a permanent red.

## Observed

A consuming corpus (`candosa/cerebro`) accepted a witness policy deriving
evidence only from `race: true` invocations, then adopted a standing project
rule barring race-enabled test runs. Every `check` since answers
`passed: false` with roughly three hundred bindings bucketed `broken`, each
carrying the identical reason («witness evidence derives only from race
invocations; cover its package with a race: true invocation»), plus
`redsOmitted` in the hundreds. The one load-bearing fact — the
`witnessSelectionProblem` line stating that the selection covers no expected
witness at all — arrives at the end of the response, after the flood.

Two consequences: the pass/fail verdict is permanently uninformative for that
corpus (its commit discipline ritually records "reds are the race policy"), and
the response shape buries a single policy-level cause under hundreds of
per-requirement restatements of it.

## Shape

1. **Verdict arm.** When the witness-eligible selection covers zero expected
   witnesses, the check's verdict should say that once, as its own state —
   policy-blocked, distinct from genuinely broken bindings — with the
   per-requirement list available behind an opt-in view rather than inlined.
2. **Policy expressibility — settled: it is not expressible.**
   `proto/stipulator/v1/policy.proto:111` states the rule at the tool level:
   «Only race-enabled invocations can grant Go witness evidence; a non-race
   invocation contributes suite health alone.» A corpus whose single
   invocation carries `race: false` can therefore never produce witness
   evidence, and the race detector requires `CGO_ENABLED=1` — so every
   repository where cgo is off the table (musl/static hermetic builds,
   unsupported targets, resource policies barring race instrumentation's
   memory multiple) is structurally excluded from an informative verdict. The
   strength ordering is real — a race-enabled pass is strictly stronger
   evidence — but as the only eligibility class it conflates evidence
   strength with evidence existence: for single-threaded semantic
   requirements a plain pass is the relevant evidence, and the all-red
   failure mode teaches consumers to ignore the gate. Admissible shapes:
   witness evidence carrying its invocation class (plain vs race) with a
   tiered verdict — concurrency-class requirements stay red without race
   witnesses, the rest green at the plain tier with the race tier reported
   unattested — or minimally, a policy bit admitting plain-invocation
   witnesses with the downgrade recorded on every witness so the tier is
   auditable rather than laundered.
