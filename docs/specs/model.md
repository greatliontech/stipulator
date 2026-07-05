# Intermediate representation

The IR is the compiled form of the corpus: a graph whose nodes are documents,
sections, requirements, terms, notes, and annotations, and whose edges are
typed. It is the sole machine-consumed form of the spec — every downstream
operation (chunking, diffing, coverage, gating) reads the IR, never the
markdown.

## Graph

**REQ-model-graph** (wire): The IR MUST represent requirements, terms,
notes, and annotations as nodes with typed edges `reference`, `uses-term`,
`refines`, `depends`, and `supersedes`, and carry document and section
structure as location metadata; graph edges hold identity-bearing endpoints
only, with references originating from identity-less blocks recorded on the
block's own node.

**REQ-model-canonical-order** (wire): Every collection in the IR MUST be
canonically ordered — identified nodes by identifier, location metadata by
path — so that corpus enumeration order is not observable in the IR.

## Identity

**REQ-model-identity** (invariant): An identity — a requirement identifier or
a term name — MUST denote the same conceptual object for the lifetime of the
corpus; identities are never reassigned.

**REQ-model-tombstones** (behavior): Retiring an identity MUST append it to
the tombstone registry at `.stipulator/tombstones.textproto`, and compilation
rejects a corpus that declares a tombstoned identity, compared
case-insensitively.

**REQ-model-source** (wire): Each requirement, term, note, and annotation
MUST carry its original markdown source, for rendering fidelity in bundles
and views; source is carried metadata, never hashed.

**REQ-model-content-hash** (wire): Each requirement and term MUST carry a
content hash computed over its canonical text: the lead paragraph excluding
the lead-in span and metadata parenthetical, followed by the payload blocks
in order, rendered to plain text, Unicode-NFC normalized, whitespace
collapsed.

**REQ-model-location-metadata** (invariant): File path, section path, and
source position MUST NOT contribute to identity, content hashes, closures, or
evidence pins; they are location metadata carried for reporting only.

**REQ-model-layout-independence** (invariant): Two corpora containing the
same blocks partitioned differently into files and sections MUST compile to
IRs identical modulo location metadata.

## Hashing

**REQ-model-hash-canonical-form** (wire): Every hash observable in
stipulator's outputs or stored records MUST be defined over an explicitly
specified canonical form; serialized protobuf bytes are not a canonical form.

**REQ-model-hash-func** (wire, refines REQ-model-hash-canonical-form): Every
observable hash MUST be the SHA-256 digest of the UTF-8 bytes of the
canonical form, rendered as sixty-four lowercase hexadecimal characters.

## Closure and bundles

**REQ-model-closure** (behavior): The closure of a requirement set MUST be
its transitive expansion over `uses-term`, `reference`, `refines`, and
`depends` edges.

**REQ-model-bundle** (behavior): A bundle MUST contain the requested
requirements, their closure expanded to a fixed point over requirement
edges and the references carried by included notes and annotations, the
terms used, and the note and annotation nodes attached to the enclosing
sections — such that every requirement identifier occurring in the bundle,
and every term name used by its requirements and terms, resolves within the
bundle.
