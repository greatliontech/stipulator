# Witness serving cannot catch property-suite flake

Consumer report (bldc, 2026-09-02): a seed-dependent property-test
defect (a rapid-driven perturbation site naming an attribute its
block does not carry — a panic only when that site was drawn)
survived three green `go test ./... -count=1` runs AND the check
leg: the round's `check` reported testsServed 1026 against
testsExecuted 84, so the flaky witness was served from cache on every
pass. A random-seed property suite passing once is not evidence it
passes; served verdicts compound that. Ask: a per-witness marker (or
a classification the witness classifier can derive — a body calling a
property driver) that lowers or disables serving for random-seeded
witnesses, so they re-execute on every check. Reproduction: bldc
internal/record/lang TestEmitLinePreservationProperty at the pre-fix
state with -rapid.seed=11290191475805212521.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
