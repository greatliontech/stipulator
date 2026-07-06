# Kill-sheets carry no toolchain pin

Lands: when the determinism harness chunk of the active plan begins.

A kill-sheet is the one persisted measurement in the system, and its pins
(body hash, witness set, operator set, budget — REQ-harden-records) all
describe inputs stipulator controls. The toolchain is an input it does
not: a Go version upgrade changes compilation, inlining, and timing, so
the same body under the same witnesses can kill differently — with no pin
moving, the stale sheet reads current.

The shape hash already acknowledges this class for declarations
(REQ-go-shape-hash: "the rendering is toolchain-versioned"). The sheet
should pin the toolchain identity (at minimum the Go version; possibly
GOOS/GOARCH for platform-sensitive witnesses) under the same covering
rule as the other pins. Fits the determinism harness: reproducibility of
sheet measurements is that chunk's subject.

The worked-out guard vocabulary exists in pew
(`github.com/thegrumpylion/pew`, docs/spec.md §7): validity =
closure ∧ runtime-inputs ∧ toolchain ∧ machine ∧ buildconfig, each an
exact-equality guard on recorded provenance, with verdicts
valid/stale/unverifiable. The sheet needs a subset (toolchain,
buildconfig; machine only if timing-sensitive kills are ever admitted) —
crib the semantics rather than reinvent them.
