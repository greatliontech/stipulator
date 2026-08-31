# Gapped requirements carry no content hash — spec edits under them are invisible to pin

## Symptom

A requirement covered only by a gap record (declared gap, no bound
witness) participates in no pin: `pin` derives content hashes from
bound claims, and a gap is not a claim. Editing the requirement's spec
text — strengthening, weakening, or redefining the contract — leaves
`pin` and `check` fully green with nothing awaiting re-consent.

## Failure mode

1. REQ-x is declared gapped (`gap` record: "enforcement lands with
   chunk N").
2. The spec text of REQ-x is edited — say the contract is weakened, or
   its meaning drifts during an unrelated spec amendment.
3. `stipulator check` stays green; `pin` reports nothing differing.
   The gap record still cites the old contract's landing condition,
   now attached to text that says something else.
4. When the gap finally fires and a witness binds, the pin consents to
   the *drifted* text as if it were the originally gapped contract —
   the re-consent ceremony that guards every bound requirement's edits
   never ran for any edit made during the gapped window.

The gapped window is exactly when a spec is most likely to be edited
(the contract is promoted ahead of its code), and it is exactly the
window with zero edit visibility.

## Ask

Gap records should carry the requirement's content hash at declaration
time, and `check`/`pin` should surface a gapped requirement whose
current text no longer matches its declaration-time hash — the same
awaiting-re-consent posture bound requirements get, with re-consent
re-stamping the gap record.

Lands: with cross-tool train chunk 114 (gofresh
docs/plans/cross-tool-train.md — the train's stipulator chunk;
disposition at its triage gate).
