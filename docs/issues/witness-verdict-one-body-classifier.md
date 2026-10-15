# Two body scans feed classification and seeding

Lands: tooling-redesign 7.8

`internal/backends/golang/golang.go` now has two scans, not the originally
reported three: `classifyWitness` inspects the resolved body's proof,
property and near-miss facts, while `bodyFactsOf` memoizes driver and callee
facts for the seeding walk. `bodyDrivesRunner` is gone. `seededInAnyView`
already folds serving across selected views; complete source certificates
cover those dependencies.

The original classifier work exposed proof/fuzz branches bypassing the
seeding union. Preserve the repaired distinction: class comes from the
resolved declaration; never-serve is the union over every relevant view.
The remaining opportunity is one per-view body-fact derivation consumed by
both readers, with selection and source generation in its identity, followed
by the explicit symbol-level fold. Do not widen dependency/interface reach or
erase generic-constraint refusals as a side effect of consolidation. Measure
the repeated work and pin differing per-view bodies and explanation sites.
