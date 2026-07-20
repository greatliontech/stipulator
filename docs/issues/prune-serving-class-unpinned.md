# Call-path identity choices without dedicated pins

Lands: when the witness path gains an execution-class seam or
observability hook a test can assert against.

Two conforming call-path choices no test pins directly; a mutation
swapping either callee would survive the suite:

REQ-gap-resolved-pruned mandates that the resolved-record evaluation
takes serving-class witness evidence — never a whole policy execution
demanded for pruning alone. Both paths conform (CLI `witnessRun` →
`golang.RunWitnesses`; MCP `verifyPipeline` → `runTests` →
`RunWitnesses`), and the serving behavior itself is pinned by the
REQ-core-one-execution tests on `RunWitnesses`. But no test pins the
*call-path identity*: a mutation swapping prune's witness source to
`ExecutePolicyWitnessed` would survive every current test. The prune
paths have no seam that exposes which execution class ran, and an e2e
distinguishing serve-vs-execute by observable side effects alone is
brittle. When an execution-class observability hook exists (the
response contract work is a candidate home), pin the prune paths to the
serving class directly.

The same class: the partitions tool's export form calls
`ProtoUncapped()` — pinned at the facts layer (the method is uncapped),
but a tool-seam swap back to the capped `Proto()` is undetectable with
small fixtures; a fixture exceeding OverlapCap through the MCP harness
needs a 12-component closure, disproportionate today.
