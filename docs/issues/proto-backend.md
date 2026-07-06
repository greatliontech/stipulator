# Protobuf backend — deferred indefinitely

Lands: when a corpus needs wire evidence that shape pins and Go witnesses
cannot cover — descriptor-level verification of protos consumed by parties
outside the repository.

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
