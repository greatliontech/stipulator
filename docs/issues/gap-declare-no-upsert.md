# `gap` refuses to update an existing declaration, forcing hand-edited textproto

Lands: when the gap-authoring ergonomics are revisited (pairs with the `bind` apply-immediately model).

Re-declaring a gap for a requirement that already has one is refused:

    gap(requirement=REQ-x, covered=REQ-x, reason="...updated...")
    -> "a gap for REQ-x already exists at .stipulator/gaps/corpus-x.textproto"

This is asymmetric with `bind`, which applies immediately and updates the record in place. A gap's
`reason` is not write-once — it evolves as the code does. Concretely: a batch of requirements were
gapped while unbuilt with reason "not yet implemented; spec drafted ahead of its code". Once the
code landed, the accurate reason became "implemented; awaiting a witness shape not yet shipped". The
only way to correct the stale reason was to hand-edit each `.stipulator/gaps/*.textproto` — exactly
the brittle manual-edit class the tool exists to remove (the same motivation as overlay hardening
over `sed`-and-stage).

The refusal also has no escape hatch: there is no `--force`/upsert flag, so even a deliberate
re-declaration must go through the filesystem.

Proposed resolution: make `gap` upsert by default (declare-or-update), or add an explicit
`--force` that overwrites the existing record's reason and landing condition. Either restores parity
with `bind` and keeps gap reasons a first-class, tool-authored field rather than a hand-edited one.
A guard worth keeping: if an upsert changes the landing condition (not just the reason), surface the
old→new landing so a silent retarget is visible.

The same asymmetry now applies to attestation records: `attest
requirement` refuses duplicates with "replace it deliberately", but no
replace or retract verb exists — evolving a judgment forces hand-edited
textproto.
