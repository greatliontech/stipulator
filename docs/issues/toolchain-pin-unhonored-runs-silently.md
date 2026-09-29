# A policy toolchain pin the environment cannot select runs silently on another toolchain

Field report from tugboat (filed uncommitted in this tree, the
standing channel), 2026-09-29, stipulator at e9254e7 / gofresh
v0.107.0-era binaries.

tugboat's accepted policy (`.stipulator/policy.textproto`) pins every
invocation to `toolchain: "go1.26.5-dst.6"` — the field the proto
documents as "Pin-at-load. Present: the required toolchain selection,
exported as GOTOOLCHAIN to the invocation." The installed toolchain is
go1.27.0-dst.14 (`~/.local/bin/go` → `~/.local/godst/current` →
go1.27.0-dst.14; ~/.local/godst carries dst.10–dst.14 and no 1.26
flavor). `stipulator check` over that policy ran to its execution
phase and beyond with no refusal and no diagnostic naming the pin:
the run's own faces say `under go1.27.0-dst.14` (the gofresh
toolchain-audit lines) — every witness executed and judged on a
toolchain the policy does not name.

The mechanism, probed: the machine's `go` on PATH is a wrapper that
overrides the switch variable outright —

    $ cat ~/.local/bin/go
    #!/bin/sh
    GOROOT= GOTOOLCHAIN=local exec "/home/nikolas/.local/godst/current/go/bin/go" "$@"

    $ GOTOOLCHAIN=go1.26.5-dst.6 go version
    go version go1.27.0-dst.14 linux/amd64   (exit 0)

— so an exported pin can never select anything on this machine (the
PATH link `go1.26.5-dst.6` the policy's comment relied on also
dangles: its target checkout is gone), and nothing after the export
compares the effective toolchain against the pin. The pin is therefore a
record with no enforcement: the consumer believes its evidence is
pinned to go1.26.5-dst.6 and its witnesses are being judged on
go1.27.0-dst.14, with the memo store keyed to whatever the sampled
toolchain was.

Expected: a pin the effective toolchain does not satisfy refuses at
load — the sampled `go env GOVERSION` (or the runner's toolchain
sample) compared against the pin, the refusal naming both and the
remedy (`policy` re-accept under the current toolchain, or install
the pinned one). pew already refuses toolchain skew on its recordings
(train chunk 53's class); this is the same class on the policy side.
Fleet-home question for triage: the export-and-sample is gofresh's
runner now (every go child rides gofresh's policy, d95424d) — the
comparison may belong there once, with stipulator's pin as its
input.

Consumer-side: tugboat re-accepts its policy under go1.27.0-dst.14 in
the same attended change set that lands its vouch set (the pin edit
is consent-bearing), so the stale pin does not persist on its side
regardless.

Lands: cross-tool train chunk 299 (gofresh docs/plans/cross-tool-train.md —
chartered at gofresh chunk 289's record, 2026-09-29; soundness first, heading
stipulator's order: the sampled toolchain compared against the pin at load, a
mismatch refusing and naming both and the remedy; the comparison is
stipulator's — the pin's consumer — gofresh has no pin concept).
