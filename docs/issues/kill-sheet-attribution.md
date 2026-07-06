# Kill-sheets attribute any test failure to the mutant, so counts are not reproducible

Lands: when the determinism harness chunk of the active plan begins.

`RunMutant` counts every non-build test failure as a kill, and (per
REQ-harden-operators, deliberately) a timed-out run as a kill. An
unrelated flake, a loaded machine, or environmental noise during a sweep
therefore converts would-be survivors into kills — the sheet's durable
claim ("no test vouching for this body noticed the breakage") can be
inflated without any signal.

Anchor: successive sweeps produced sheets for
`stipulate/structural.NoImport` at the identical body hash and witness
set reading 24/24 killed, 9/24, 24/24, and finally 9/24 again. The 24/24
readings were false kills from environmental failures attributed to the
mutant (an unregistered test-binary flag; earlier, unknown noise); 9/24
is the reproducible measurement under the fixed engine. Nothing in any
record distinguished a sound sheet from a corrupted one — only re-runs
did, and each corruption *inflated* kills, the flattering direction.

Directions the harness could take: re-run survivors and kills for
confirmation (kills are cheap to confirm — the failing test is known);
distinguish "test failed" from "bound witness failed" by parsing
`go test -json` output for the witness set; or record per-mutant killer
attribution so a kill by an unexpected test is visible.
