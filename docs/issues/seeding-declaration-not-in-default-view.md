# Seeding refuses in-module interface declarations as "not in the default view" — ~560 witnesses per check, no build tag involved

Field report from tugboat (filed uncommitted in this tree, the
standing channel), 2026-09-29, gofresh v0.107.0-era stipulator
(e9254e7) over tugboat at 3c8f32c, toolchain go1.27.0-dst.14.

Two full `stipulator check` runs (18:07 and 18:53, the second after
tugboat's policy gained its standing vouch set) report the same
dominant uncacheable family, byte-identical counts:

    223  uncacheable: unclassifiable seeding: executes every run, never served (absence of proof never serves): declaration of (github.com/greatliontech/tugboat/transport.Transport).Send is not in the "default" view of in-module package github.com/greatliontech/tugboat/transport
    123  ... declaration of (github.com/greatliontech/tugboat/wal.FS).MkdirAll is not in the "default" view of in-module package github.com/greatliontech/tugboat/wal
    115  ... declaration of (github.com/greatliontech/tugboat/internal/raft.Logger).Infof is not in the "default" view of in-module package github.com/greatliontech/tugboat/internal/raft
     55  ... declaration of (github.com/greatliontech/tugboat/internal/raft.Storage).InitialState is not in the "default" view of in-module package github.com/greatliontech/tugboat/internal/raft
     45  ... declaration of (github.com/greatliontech/tugboat/internal/raft.stateMachine).Step is not in the "default" view of in-module package github.com/greatliontech/tugboat/internal/raft
     43  ... declaration of (interface).Fatalf is not in the "default" view of in-module package github.com/greatliontech/tugboat/node
    ... and "337 more across 39 reasons" (the summary face caps the list)

Every named declaration lives in an UNTAGGED non-test file of its
package: `transport.Transport.Send` is transport/transport.go:118,
`wal.FS.MkdirAll` the wal seam interface, `raft.Logger`,
`raft.Storage`, `raft.stateMachine` the core's plain files. None of
transport, wal, internal/raft, raftstore carries a `//go:build` file
outside `_test.go` (node's, wal's, lifecycle's, placement's tagged
files are all test files: the dst bubbles and sweeps). So the
"default" view of each package contains these declarations by
construction, and the seeding walk is answering that it does not.

What differs from a plain analysis: the policy's witness-eligible
invocation is `dst-race` (`-tags dst -race`), so the test binary's
view is the "dst,race" selection while the reason names the
"default" view of the in-module package — the two views of one
package appear to be keyed apart, and a method declared identically
in both is looked up in the one the seeding did not load (or loaded
under the other selection's key). The family is confined to
interface-method and unexported-type-method declarations reached
through the test's call graph (a declaration of `(interface).Fatalf`
— the testing.TB-shaped parameter — is among them), which points at
the receiver-declaration lookup rather than the package load.

Cost: this is the whole corpus. With the dst selection ALSO
unaudited (`dst-selection-walk-trigger-fired-tugboat.md`), nothing
in tugboat's 1073 witnesses can be served today, and the family
would remain after the walk lands unless it is a symptom of the
same unwalked-selection state (the refusal wording says "absence of
proof never serves", which reads as a fail-closed classification
rather than a lookup fault — if the family is BY DESIGN the
unaudited selection's shadow on in-module declarations, the reason
should say so and name the walk as the remedy, since a consumer
reading it today goes looking for a build-tag problem that does not
exist).

Reproduce: `stipulator check` in tugboat (16 min; the reasons are on
the summary face). `stipulator explain --reason '<line>'` answers
"no culprit parsed from the reason; pass --package and --symbol" —
the seeding family has no explain derivation (a second, smaller
finding: every uncacheable reason class should be explainable or
say which verb explains it).

Lands: cross-tool train chunk 298 (gofresh docs/plans/cross-tool-train.md —
chartered at gofresh chunk 289's record, 2026-09-29: the seeding walk's
declaration lookup under the invocation's own selection, reproduced over
tugboat first; the explain derivation for the seeding family rides it).
Filed in gofresh's tree by the reporter; moved here, its owning repo.
