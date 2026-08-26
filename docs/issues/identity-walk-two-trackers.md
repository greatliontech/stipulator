# Attachment and extent walk the document twice with different reset rules

Reviewer-surfaced (context-extent change set, 2026-08-26): "which
identity does this block belong to" is answered by two independent
state machines — profile/transform.go's `lastIdentity` (attachment:
reset by headings, ordinary paragraphs, and every default block, so a
note attaches only across immediate adjacency) and
compile/extract.go's `openExtent` (extent: reset only by identity
leads, headings, and thematic breaks, so context spans intervening
blocks). The relationship that makes the model coherent — attachment
is a strict subset of extent membership — holds today by coincidence
of two switch statements in two packages over two passes of one
document; nothing checks it, and either reset table can drift alone.

Collapse: one "current identity" walk producing both answers from one
reset table (a narrow attachment window and a wide extent window), so
the subset relationship is structural. Requires either the profile
layer exporting the walk or the extent moving into profile alongside
attachment.

Invariants preserved: REQ-profile-note's adjacency-only attachment;
REQ-profile-context-extent's boundary set; a section-attached block
joins no extent.

Lands: the next chunk that touches the profile walk, attachment
semantics, or the extent boundary set.
