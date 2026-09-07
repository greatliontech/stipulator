# The extractor keeps two context-block shapes and two IR loops for one concept

`internal/compile/extract.go` declares `noteBlock` and `annBlock` with
the same segs/source/location fields (the note adds its attachment),
extends an identity's extent from two identical call sites, and
`compile.go` walks the two lists through near-identical loops
(canonical text, source, reference check, orphan-keyword check,
location) that differ only in the attachment set. The profile now
hands both down as one shape — a context block with a Context
identity, the note additionally attached — so the extractor's split is
a vestige of the two-walk era.

The collapse: one context-block type carrying an optional attachment,
one extent extension, one IR loop emitting a note or an annotation by
the attachment's presence. Invariants preserved: REQ-profile-note's
attachment on the note IR, REQ-profile-annotations' section attachment,
REQ-profile-context-extent's extent membership and hash contribution
(the extent segs order is byte-identical either way — pin with the
existing extent hash tests).

Lands: the next change to the extractor's block types or the note/annotation IR loops.
