# Issues

Deferred follow-ups. Each carries a `Lands:` trigger saying when it should be pulled in.

- **[attestation-refusal-names-no-reclassification](attestation-refusal-names-no-reclassification.md)** —
  the (invariant, MUST) attestation refusal is right but names no remedy; point the author at
  reclassification (a closed enumeration is a wire requirement). *Lands: user decision.*
- **[identity-walk-two-trackers](identity-walk-two-trackers.md)** — attachment and extent answer
  "whose block is this" via two independent reset tables in two packages; the subset relationship
  holds by coincidence. Collapse to one walk with two windows. *Lands: cross-tool train chunk 131.*
- **[witness-runner-environment-divergence](witness-runner-environment-divergence.md)** — a witness
  red only inside the runner; the environment delta is uninspectable. *Lands: cross-tool train chunk 114.*
- **[cli-repeated-flag-claims-silently-dropped](cli-repeated-flag-claims-silently-dropped.md)** — CLI
  `bind` with repeated `--req`/`--symbol` flags exits 0 and writes only the last claim; batch or
  refuse, never accept-and-drop. *Lands: with train chunk 114.*
- **[gapped-requirement-spec-edits-invisible-to-pin](gapped-requirement-spec-edits-invisible-to-pin.md)** —
  gap records carry no content hash, so spec edits under a gapped requirement never trigger
  pin re-consent; the eventual binding consents to drifted text as the original contract.
  *Lands: with train chunk 114.*
- **[proto-backend](proto-backend.md)** — descriptor-level verification via protocompile;
  spec exists, five requirements gapped. *Lands: capability charter (gofresh docs/plans/capability-charters.md) — activates when a corpus needs wire evidence shape pins and Go witnesses cannot cover.*
- **[out-of-process-backends](out-of-process-backends.md)** — trusted backend surfaces can move
  behind a wire protocol while Stipulator continues deriving evidence in the current run;
  mutation findings remain gomutant-owned. *Lands: capability charter — activates when a second language backend is planned.*
- **[prover-trust-tiers](prover-trust-tiers.md)** — the proof rung assumes near-sound provers;
  a heuristic analyzer must not inherit it. *Lands: capability charter — activates when a heuristic analyzer prover is proposed.*

- **[performance-evidence-axis](performance-evidence-axis.md)** — no clause kind or evidence
  class measures performance; pew recordings (guard-derived validity) are the binding-pin
  model applied to measurements and slot in without bending the trust model. *Lands: capability charter — activates when a corpus declares a performance requirement.*

- **[structural-call-absence-verb](structural-call-absence-verb.md)** — "never constructs
  X" structural clauses have no verb: NoImport is transitive (stdlib forbiddance fails
  through any real dependency) and the shape verbs state presence, not capability absence;
  a direct-call-absence verb (structural.NoCall) would carry them. *Lands: capability charter — activates when a structural requirement needs a call-absence proof the signature/import verbs cannot carry.*

- **[supersede-removed-source-one-step](supersede-removed-source-one-step.md)** — superseding a
  requirement removed in the same edit needs three writes (drop edges, retire, re-add); a one-step
  removed-source flow. *Lands: user decision (bldc consumer report 2026-09-02).*
- **[normative-keyword-lint-timing-and-remedy](normative-keyword-lint-timing-and-remedy.md)** — the
  one-keyword rule surfaces at the next write op; the message names the count, not the remedy.
  *Lands: user decision (bldc consumer report 2026-09-02).*
- **[pin-req-unchanged-text-wording](pin-req-unchanged-text-wording.md)** — "pins current" for
  unchanged text reads as a skipped re-consent. *Lands: user decision (bldc consumer report 2026-09-02).*
- **[clause-granular-binding-claims](clause-granular-binding-claims.md)** — coverage's unit is the
  requirement, so a multi-clause requirement reads green with a clause unenforced; clause-naming
  claims. *Lands: user decision (bldc consumer report 2026-09-02).*
- **[content-hash-function-versioning](content-hash-function-versioning.md)** — a tool rebuild moved
  four content hashes over unchanged text; the re-consent is indistinguishable from an amendment.
  *Lands: user decision (bldc consumer report 2026-09-02).*
- **[refines-multiple-targets](refines-multiple-targets.md)** — `refines` admits one target; the
  second relationship is lost. *Lands: user decision (bldc consumer report 2026-09-02).*
- **[cli-verify-view-path-and-explain](cli-verify-view-path-and-explain.md)** — the symbol-claims
  query is MCP-only; CLI `verify` lacks `--view`/`--path`, no CLI `explain`.
  *Lands: user decision (bldc consumer report 2026-09-02).*
- **[gap-covered-unknown-id-at-declare](gap-covered-unknown-id-at-declare.md)** — `gap` accepts prose
  in `covered` silently (dangling forever, check green); refuse at declare.
  *Lands: user decision (bldc consumer report 2026-09-02).*
- **[check-green-over-witness-failure](check-green-over-witness-failure.md)** — `check` exited green
  naming a failed witness as uncacheable-blocked; owner to confirm against the `healthy` term.
  *Lands: user decision (bldc consumer report 2026-09-02).*
- **[property-suite-witness-serving](property-suite-witness-serving.md)** — served witnesses hide
  random-seed property flake (1026 served / 84 executed); re-execute property witnesses.
  *Lands: user decision (bldc consumer report 2026-09-02).*
