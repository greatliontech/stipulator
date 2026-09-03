# gomutant's ephemeral oracle refuses a green internal/cmd baseline

`gomutant ephemeral --test-pkg …/internal/cmd --run
TestCommandsRefuseHygieneBeforeAnyWitness` refuses with "the named test
does not pass on the unmutated tree", while the test passes under `go
test` in the tree, in a copied tree, under `-json`, `GOWORK=off`, a
fresh session, and a closed stdin — the oracle's own shape as far as it
is visible. The sibling `TestGateVocabularyRefusesBeforeAnyWitness` in
the same package, the same fixture shape and the same in-process
command execution, is probeable and kills. The oracle exposes no
baseline output, so the disagreement cannot be bisected from here.

Effect: the CLI hygiene leg of REQ-check-preparation (gate, verify,
prune refusing before any child) carries no mutation evidence beyond
the plain run; the mechanism it exercises (`refuseHygiene`) is shared
with the probeable gate pin.

The gomutant chunk owns the oracle: expose the baseline's output on a
refusal, and explain what the oracle's environment adds that the plain
run lacks.

Lands: cross-tool train chunk 156 (gofresh docs/plans/cross-tool-train.md).
