# Superseding a requirement removed in the same edit is a chicken-and-egg

Consumer report (bldc, 2026-08-29): a wholesale spec rewrite removes
requirement A and declares B with `supersedes A`. Corpus compile
refuses B's edge — "supersedes A, which is neither declared nor
tombstoned" (compile.go) — but tombstoning A first IS the dispose the
author is attempting, and dispose needs the corpus to compile. The
session worked around it by temporarily dropping the edges, retiring
both ids, then re-adding the clauses: three writes for one intent.

Ask: a one-step flow for the removed-source case — "source already
removed from the spec: tombstone it and accept the successor edge" —
either as a dispose mode or as compile admitting a supersedes edge to
an id present in the record's tombstones-or-pending set.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
