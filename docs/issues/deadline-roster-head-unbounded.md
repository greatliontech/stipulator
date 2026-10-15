# The deadline roster can consume the diagnostic's residue budget

Lands: tooling-redesign 5.3

`internal/backends/golang/execute.go` collects each matching running-test
dump line in `streamState.roster`. The terminal timeout head joins that
roster before `runResidue` appends package output, aborted-test output and
stderr. The roster has no independent bound, so a head near the overall
failure-output cap can displace the residue.

No field report is needed to construct this state: synthesize a valid large
runtime roster and distinct residue in the already-planned reporting work.
Bound or share the head's allocation, state omitted entries, preserve the
deciding timeout and test attribution, and retain useful residue within the
overall cap. Raw-byte scrubbing and rune-safe cuts already exist in
`runResidue` and `residue_test.go`; this remaining obligation concerns budget
allocation, not UTF-8 serialization.
