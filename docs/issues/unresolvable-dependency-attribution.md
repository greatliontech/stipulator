# A tree referencing unresolvable dependency symbols degrades verbs without naming why

Field demonstration (chunk 112, 2026-08-29): with the working tree
referencing a gofresh export unreleased under the module's own
workspace pin, `bind` refused with the raw loader error
("undefined: closure.ToolchainSelectionNotice") and the witness
classifier silently classified the affected package's invocations 0
(TestWitnessClassProof red as "import allowlist invocation
classified 0") — the load failure surfaced as a wrong-looking
classification, not as its cause. The correlated variable (the
committed workspace resolves the dependency at the go.mod pin, not
at the developer's working copy) had to be re-derived by hand; the
cross-repo staging protocol pays this on every substrate-export
chunk.

Resolution shape, per the automation-over-configuration directive:
detect, don't configure. When a backend package load fails, classify
the failure — an unresolvable imported symbol is a dependency-
resolution state, and every consuming verb (bind's symbol
resolution, the witness classifier, check's execution report) names
it as such, with the module workspace that resolved the dependency
and the pinned version, instead of leaking the compile error or a
degraded verdict. The verbs still refuse/degrade — fail-closed
stands — but the answer teaches the state, so the operator's next
step (release the dependency, bump the pin) is a decision, not a
diagnosis.

Lands: with train chunk 114 (stipulator runner-environment
inspectability — the same diagnostics class: name the correlated
variable)
