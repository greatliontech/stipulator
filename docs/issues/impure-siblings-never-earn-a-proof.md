# Package-producing evidence for unasserted observing siblings

Lands: tooling-redesign 5.2d

The original report describes two unasserted, runtime-input-reading tests
sharing a package process and re-executing every run: the singleton proof
candidate requires exactly one selected top-level runnable. The relevant
source is `internal/backends/golang`'s package legs, proof-candidate selection
and publication judgment; REQ-evidence-witness-freshness-purity states the
singleton rule. This is not missing source-resolution certificate coverage.

The shared package-producing form now closes one execution over an explicit,
complete subject set with one process-wide observation. Stipulator must adopt
that boundary and amend its sole-runnable proof rule together: every executed
subject, including non-cacheable tests and examples, contributes to support
preparation before the process starts. Unsupported members refuse support for
the whole process. The former isolate-each-once proposal is not that migration.
No current analysis may invent a missed producing obligation or lend one
subject's support to its sibling. Preserve the native two-observing-subject,
singleton, failure and cancellation controls in consumer-level regressions.

Each original or retry process remains a distinct candidate. A witness selects
one producing execution; merging an abnormal original's observation into a
successful retry would destroy the retry's independent evidence. The package
publication gate waits for its owed work but does not union candidate evidence.
Preserve actual failure rows and diagnostics separately from witness eligibility.

Complete process/build descriptions and retained artifacts belong to this
adoption. Secondary constituents: 5.2e covers restart and historical non-upgrade;
5.3 preserves retry durability, witness tiers and accounts. Native controls do
not establish newly served Stipulator siblings before the consumer migration.
