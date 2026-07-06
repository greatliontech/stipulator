# Determinism witness covers compile→verify→evaluate, not every operation

Lands: when the determinism harness chunk of the active plan begins.

REQ-core-determinism ranges over **every** stipulator operation given
byte-identical inputs. The property witness
(`internal/coverage.TestPropPipelineDeterminism`) quantifies the
compile→verify→evaluate pipeline, and the record verbs get second-run
identity through `internal/author.TestPropVerbsWriteOnlyRecords`. No
determinism witness exercises: fmt/index generation, bundle/closure
export, facts/slice, IR diff, or the harden engine's kill-sheet
rendering.

A property witness never exhausts a for-all, so the binding is
policy-legal; this tracks the *operation-coverage* residue so it survives
if the harness chunk's scope shrinks. The harness should either extend
the quantification to the remaining operations or record per-operation
anchors.
