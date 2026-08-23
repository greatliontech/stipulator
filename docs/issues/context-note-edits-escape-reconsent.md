# Context-note edits escape re-consent — the content hash covers only the requirement paragraph

Reviewer-verified on tugboat (2026-08-23, chunk-3b round 2): a
requirement's content_hash covers the bolded REQ paragraph alone;
layout and vocabulary blocks that follow it (REQ-node-snapshot-
encoding's verdict-code table, header/chunk layouts) are modeled as
unattached Context notes and are NOT hashed. Editing the wire
vocabulary — adding verdict code 24 in that instance — left all
thirteen bindings "fresh" with no re-consent demanded, while a body
edit correctly re-pinned. Wire tables ARE contract (an encoding
change is exactly what re-consent exists to catch), so the escape is
a soundness gap in the pin model, not a hygiene nit.

Fix shape: fold a requirement's context-note extent into its content
hash, or hash the notes separately with the same re-consent flow.
Migration is one re-consent sweep on upgrade (every requirement
carrying notes re-pins once).

Lands: with the tool-phase stipulator visit.
