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
