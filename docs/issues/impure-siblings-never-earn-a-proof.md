# Package-producing evidence for unasserted observing siblings

Lands: tooling-redesign 5.2c

The original report describes two unasserted, runtime-input-reading tests
sharing a package process and re-executing every run: the singleton proof
candidate requires exactly one selected top-level runnable. The relevant
source is `internal/backends/golang`'s package legs, proof-candidate selection
and publication judgment; REQ-evidence-witness-freshness-purity states the
singleton rule. This is not missing source-resolution certificate coverage.

The former proposal to isolate each impure sibling once is not the approved
package migration. Specify and release the minimal shared producing form
for the actual contributing subjects and processes, preserving the singleton
form, separate retry executions and independent package closing. No current
analysis may invent a missed producing obligation or lend one subject's
support to its sibling. Use a two-observing-subject package plus singleton,
failure and cancellation controls to decide which evidence is admissible.

Secondary constituents: 5.2d adopts that form and complete process/build
descriptions; 5.2e covers restart and historical non-upgrade; 5.3 preserves
retry durability, witness tiers and accounts. No claim of new runtime
validation or newly served siblings is made by this filing.
