# Gofresh package-producing adoption remains after the dependency bump

Lands: tooling-redesign 5.2d

The 2026-10-12 fleet sweep reported Stipulator on Gofresh v0.115.1 against
released v0.116.1. Those versions identify the report, not today's checkout:
`go.mod` now pins v0.116.3. A current dependency version does not establish
adoption of the package-producing boundary.

`internal/backends/golang/observe.go` still composes producer frames and
`ProducerIngest` through the legacy producing path. `derive.go`'s
`pgoBuildInputs` does not cover omitted auto-PGO as required by the live
contradicted gap on REQ-evidence-witness-freshness-inputs. Complete selections,
module mode, physical/dotted declaration identities and cross-view source
certificates are implemented; they are not this producing migration.

Prerequisite: 5.2c's minimal shared package-producing form. Adopt complete
compilation and runtime descriptions, retained artifacts and caller-owned
construction cancellation across producing, finalizing, checking and
publication. Cover omitted/explicit auto, off/path and missing/unreadable
profiles. A dependency-only bump leaving the producer degraded is not closure.
Install the clean consumer and restart long-lived readers after acceptance.
Secondary constituent: 5.2e's restart and historical non-upgrade fixtures.
