# Protobuf backend — deferred

Lands: when a party outside this repository consumes stipulator's
protos — a module importing gen/stipulator/v1, or a non-Go consumer
of proto/stipulator/v1 — so that wire evidence shape pins and Go
witnesses cannot cover is owed to it.

`docs/specs/backends/proto.md` states the contract (in-process
protocompile, symbol scheme, canonical descriptor shape hash, provers, no
option-derived claims); its five requirements stay gapped. Deferred because
the machinery is large, the initial cut is partial, and the marginal gain
is small: wire behavior is already witnessed by Go round-trip tests, and
schema drift is already caught by Go shape pins on the generated types'
consumers.

Design analysis to reuse when this lands:

- **Resolver is pluggable, three tiers.** Workspace-local imports + WKTs
  (protocompile `WithStandardImports`; roots read from committed buf.yaml,
  never toolchain state) covers most corpora. External deps via the buf
  module cache are deliberately out: the cache layout is undocumented and
  has changed twice (v1→v3), and verification would depend on cache
  population — colliding with REQ-core-determinism. When needed, read
  buf.lock + cache as one strategy behind the resolver interface, with
  vendored (`buf export`, committed) deps as the always-works fallback.
- **Evidence tiers.** Resolution + canonical-descriptor shape pin is the
  bulk of the value (integration tripwire); Go witnesses already cover
  behavior; provers split into a parameterless subset (existence,
  closed-enum) and parameterized assertions (reserved ranges, expected
  types) whose parameters need a committed home — a new assertion-record
  kind under `.stipulator/`, a claim grammar that deserves its own design
  pass. Reserved-range regression ("ranges never shrink") is a diff-time
  check belonging to the change model, not a static prover.
- REQ-proto-provers likely needs an amendment to match the parameterless
  initial cut before implementation starts.

Landing note (2026-08-29, pin shape reporting): the pin surfaces'
reshaped and shape-mismatch rows key on the BARE bound symbol
(records.Pin, records.ShapeMismatched) — sound while `go` is the only
backend, but a second backend binding the same symbol string would
merge two distinct rows. Backend-qualify those row keys when this
lands.

Audit 196 (2026-09-09): the corpus's two root wire clauses
(REQ-core-proto-io, REQ-model-graph) are gapped on `covered:
REQ-proto-provers`, whose own gap reads "deferred indefinitely" — a
condition that cannot fire — and twelve bindings name `backend: "proto"`,
which verification skips because no such backend is registered:
records that can never become evidence. The scheduling call is the
user's: retire docs/specs/backends/proto.md through `dispose` and drop
the twelve bindings, or retarget the two root gaps to conditions that
can fire.

Chunk 321 (2026-10-06, the CI gate): the twelve `backend: "proto"`
bindings were dropped — records no registered backend can verify are
not evidence, and the fleet's records tier requires every row current
and resolved — and the two root gaps (REQ-core-proto-io,
REQ-model-graph) retargeted from `covered: REQ-proto-provers` (a
condition that could not fire) to the adoption trigger above as a
manual condition, the five REQ-proto-* gaps on the same (they had read
"proto backend work is scheduled", an aspiration). The spec stays,
gapped on that event: an outside consumer lands the backend and
re-binds the messages. Its retirement has no trigger of its own —
nothing observable says a consumer will never come — so it is not
scheduled; a decision to retire it goes through `dispose` as a scope
change.
