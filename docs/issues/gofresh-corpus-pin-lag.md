# Gofresh package-producing adoption remains after the dependency bump

Lands: tooling-redesign 5.2d

The dependency pin in `go.mod` does not establish adoption of the
package-producing boundary.

Installation also requires the Gofresh release enforcing v4-only runtime-manifest
admission at 5.1w.4. The current v0.118.0 dependency still accepts v3. Replacing the
external converter does not repair outcomes previously decoded incorrectly: a
normally completed, purity-declared process can emit an escaped pass report after
a genuine skip, and the old converter's chunk-dependent escape handling can turn
it into a later passed row. The witness publication fold can retain that pass.
The corrected converter must not lend its interpretation to such history.

Require the released v4 admission boundary before final consumer acceptance and
installation: the native manifest decoder then rejects v1–v3 before stored
outcomes can enter serving. This uses the already-selected historical cut;
neither a new witness-envelope version nor a toolchain-flavor approval repairs
the missing producing evidence. Update manifest references with that dependency
adoption and preserve a regression for the old-record refusal.

The v0.117.5 runtime-manifest adoption also exposes a concrete publication
blocker in that boundary. `TestGoRunWitnessesBuildCacheAndTempReadsStayCacheable`
executes successfully but refuses publication with `runtimeinputs (moved: env
PWD)`: ingestion describes the package process, while legacy revalidation
uses the engine environment. Version 3 correctly retains an observed PWD;
omitting it again would lose a runtime dependency. Legacy producer overrides
require the analysis environment and cannot express the separate coordinate.
Land the dependency adoption with the native build/runtime migration at
5.2d.2, preserving the fixture's cold publication, warm serving and package-PWD
assertions. The PGO implementation alone cannot close this adoption gate.

`internal/backends/golang/observe.go` still composes producer frames and
`ProducerIngest` through the legacy producing path. Omitted and explicit
auto-PGO now observe selected original main packages, including absent
defaults. Immutable engine generations own their profile inputs and selection
metadata; prebuild and closing checks refuse drift, and retries derive current
inputs. Preserve these controls through the remaining migration, alongside
complete selections, module mode, physical/dotted declaration identities and
cross-view source certificates.

Adopt the released shared package-producing form with complete
compilation and runtime descriptions, retained artifacts and caller-owned
construction cancellation across producing, finalizing, checking and
publication at 5.2d.2. Preserve omitted/explicit auto, off/path,
missing/unreadable profiles and the prebuild/closing/retry regressions.
A dependency-only bump leaving the producer degraded is not closure.
Install the clean consumer and restart long-lived readers after acceptance.
Secondary constituent: 5.2e's restart and historical non-upgrade fixtures.

## Cerebro linked-implementation selection

Lands: tooling-redesign 5.1w.7 for the shared Gofresh release; 5.2d.2 for
real released-dependency adoption and migration qualification; 5.2e for the
Stipulator verification, installation and reader restart.
The shared API is on Gofresh main at `f5f02e6`; consumer integration is in the
pending clean-break migration. Neither development selection nor these source
changes establish released-consumer acceptance.

The tooling workstream owns feature delivery through the queued breaking changes,
tool verification, normal releases and installation. Cerebro's owner performs its
application E2E validation; that validation is not a tooling delivery gate.

The integration owner is the main tooling-redesign workstream. The Cerebro
author has paused further publication/integration of these reviewed handoffs:

- Gofresh `compat/exact-file-eligibility`,
  `098fe979d31ef9f5b818202ef61532884012be02`: producer-owned
  `FileInputEligibility`, using the existing classification rather than a
  consumer approximation or observation bypass.
- Stipulator `feat/cerebro-linker-selection`,
  `00759e435e1efa6d0737729305746c55da937b2f`: typed linker string assignments
  and exact environment-selected input files, including policy/report schemas,
  selection identity, observation eligibility and deciding controls.

The derived requirements are owned by `REQ-go-linker-selection` and
`REQ-go-invocation-input-files` in [the Go backend contract](../specs/backends/go.md).
Consumer qualification follows the identity and manifest from production through
selection, linking, consumption, closing, storage, reuse and retries. Declared
linker inputs do not establish actual implementation use; a manifest's assertions
are not automatically checked evidence. The handoffs remain implementation
provenance and regression evidence, not design authority.

The consumer contract is published as `candosa/cerebro` branch
`handoff/cerebro-tooling-consumer`, commit
`95f6fb15851820f22fc66102fa6b6c335e430a72`. Its README and hash-verified
40-file orientation snapshot distinguish the current source baseline
`701185cb57345bdf5194e2b270a6e6a6859e6ed5`, reviewed source S3
`d48493d0ff6d44e8fa0159b6f665ef5176dcb4ec` and published main `e71b1ec`.
The snapshot supplies the consumer contract, not executable qualification. Full
matching Git history and materialized LFS inputs belong to Cerebro's own E2E
validation, including its historical source-reader inputs; obtaining them is not
a prerequisite for delivering the tooling features.

The ordinary identity contract in `docs/specs/machinery/library-artifacts.md`
and its executable producer distinguish a library-source census from optional
whole-build certification. `cmd/gen implementation-identity` retains exact
manifest bytes outside the identified tree and emits an explicit linker assignment
whose value is `ordinary-library-source/1:` followed by their SHA-256. The census
includes production alternatives, generated runtime law, conservative assets and
module/workspace controls; it is not a selected closure or toolchain census.
Its external-replacement, workspace, overlay and modfile refusals remain producer
obligations. Endpoint regeneration detects drift but does not seal compilation.

The resulting integration must preserve these independently owned claims:

- Cerebro owns source membership, generation coherence and the relation of that
  retained manifest to the build. Stipulator transports explicitly selected typed
  linker values; Gofresh guards the actual build selection. Neither tool interprets
  an identity label or manifest assertions as implementation proof.
  The consumer invocation must run the existing producer before selecting the
  build, retain its exact output, hold inputs stable and regenerate/compare after
  the run. Reusing an old matching manifest/value pair after changing library
  source passes the receipt's equality check but misidentifies that source;
  transport alone cannot establish the producer's source-to-manifest relation.
- Stipulator resolves each declared value source once under the effective admitted
  invocation and carries the same assignments through compilation, native analysis,
  retained artifacts, retry selections and reporting. A different linked value
  with an unchanged freshness key would permit reuse for another implementation.
  Omitted identity remains omitted; the runner cannot inject one into child binaries
  whose tests deliberately exercise missing or distinct identities.
- The test-manifest variable is an oracle input, not production's identity source.
  Gofresh owns whether the exact file is raw-byte observable under the actual
  process directory/environment. A bracket alone cannot restore a cache-elided,
  excluded or projected read. Stipulator admits the candidate path and brackets it
  before execution; declaration alone creates no observed read or outcome support.
  Unrelated package tests need not read or link the library merely because the
  invocation carries those selections.
  Gate each actual retained runtime before execution and warm checking, retaining
  the environment-name/file association as well as raw eligibility.
  In particular, retained coverage preparation rewrites `GOCOVERDIR`: a declared
  old regular file can stay eligible while the process receives a different
  selecting value. Refuse that mismatch rather than rebinding the declaration.
  Native preparation permits degraded execution, so the gate remains independent
  of native registration to prevent an invalid selection from executing.
- Cerebro's real receipt tests establish actual linked use and compare release to
  SHA-256 of the retained manifest's exact bytes, separately asserting source S3,
  owner semantics and provenance. Successful `-X` alone is insufficient: Go can
  accept a target absent from that package's executable. Missing identity/manifest,
  changed bytes and a different linked identity must retain their deciding failures
  beside an identified positive control.
- Native process registration, closing and current checks preserve actual runtime
  inputs, source/build identity and the complete execution roster. Each retry gets
  its own frame and closing state, even when sharing an unchanged retained artifact.
  A restored input cannot erase established producing-span movement, and an
  eligibility answer or passing receipt cannot supply missing outcome support.

The current Cerebro policy has no linked-identity or manifest selection. Preserve
its explicit plain-witness admission, package scopes and existing environment
semantics; do not add tags/race, GOFLAGS injection, a command wrapper, purity or
semantic approvals to make integration pass. Preserve transport and native drift
controls over real retained test binaries at released-dependency adoption.
Cerebro's owner validates its producer → retained bytes → explicit linker
selection → test environment → actual receipt relationship using the delivered
features. Warm acceptance holds
the linked selection, retained manifest path and bytes, and runtime environment
fixed; changing the selecting pathname remains an observed environment change.
Historical S3 results remain historical, not a current policy or whole-tree pass.
The disclosed ordering
and neutral-profile WIP and the separate analyzer work are outside this acceptance.

The consumer's capabilities use the native producing and structured-judgment
model in the pending migration, with its policy, normalization, derivation,
execution, observation and protobuf changes kept together. Preserve
resolved linker assignments through retained builds and every retry, and check
file eligibility with the actual producing directory/environment and shared
classification owner. Eligibility creates no observation or outcome evidence;
the test must read the manifest and normal bracket/closing checks still apply.

Qualification against Gofresh `3dbc84f`, including the breaking boundary at
`f0a4f10`, reported seeded-record failures in
`TestDeriveCachedOutcomeGrantsNoHealthOrEvidence`,
`TestPublicationCancellationRetainsTheInstalledPrefix` and
`TestPruneToolStoreGC`. Preserve the reconciled seeds' native producer/store
encoding, structured judgments, original assertions and malformed/historical
controls when rechecking the released dependency. The reported passing comparison used
v0.116.3 plus the additive eligibility API; that isolates the dependency change
but does not establish acceptance on main.

Release the integrated Gofresh capability through normal release CI, update
Stipulator's real module dependency, then complete Stipulator verification,
release, installation and reader restart. The handoff's older base is not a
compatibility-release deliverable. Separate repository-analyzer work remains
incomplete; its prerequisite issue is not evidence of that work's delivery.

Until the shared capability is released, the declared v0.118.0 dependency does
not contain the exact-file API. Development qualification selects the local
implementation explicitly; isolated authoring/probe copies carry a local module
replacement where those tools intentionally disregard an ambient workspace.
These checks establish integration against that development selection, not
released dependency adoption. At 5.2d.2, recheck bindings, pins, build and execution
against the real released module pin before claiming the main-line consumer's
acceptance. Keep the module files free of a temporary compatibility pin or local
replacement in the delivered migration.
