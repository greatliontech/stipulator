# Advisory attachments

External tools may examine the binding graph and return useful material without
becoming trusted verifiers. Stipulator exports current binding surfaces as
addresses and correlates supplied advisory attachments with those addresses;
it neither requests work nor interprets a producer's method. Trusted evidence
remains evidence derived by a backend Stipulator invokes during the current
verification run.

**advisory attachment** (term): Out-of-band, non-evidence material associated
with a requirement and an exported binding surface. An attachment can inform a
reader but can never satisfy coverage or affect the gate verdict.

**binding surface** (term): One implementation symbol, the requirements it is
claimed to implement, and the `tests` and `proves` bindings associated with
those requirements. It is an address for correlation, not a request to perform
any particular analysis.

**REQ-advisory-targets** (wire): The `targets` operation MUST export every
in-corpus `implements` binding as a backend-independent binding surface,
grouping claims with the same backend and symbol, and carrying the sorted
requirement identifiers plus the sorted backend, role, and symbol of their
`tests` and `proves` bindings. Each surface carries an identifier derived
from exactly that relationship, so adding, removing, or retargeting any
participating binding changes the identifier while source changes that leave
the binding relationship intact do not. Filters can narrow the exported set
but leave an included surface and its identifier unchanged.

**REQ-advisory-surface-id** (wire): A binding-surface identifier MUST be the
REQ-model-hash-func digest of this canonical byte sequence: the ASCII domain
`stipulator-binding-surface-v1`, followed by the implementation backend and
symbol, then sorted requirement identifiers, then evidence bindings sorted by
role, backend, and symbol. Every string is encoded as its base-10 UTF-8 byte
length, one colon, then those bytes; each collection is preceded by its
base-10 element count and one colon. No separators or serialized protobuf
bytes otherwise contribute.

**REQ-advisory-attachment-format** (wire): An advisory attachment document
MUST be expressible as a protobuf message and accepted in its ProtoJSON form.
Each attachment carries a requirement identifier, binding-surface
identifier, globally namespaced kind with version, producer name, human-facing
summary, and an optional opaque protobuf `Struct` payload. Unknown kinds and
payload fields remain valid advisory material; malformed documents are
refused rather than partially read.

**REQ-advisory-correlation** (behavior): Advisory attachment documents MUST
enter only as explicit operation inputs, never through a discovered or
configured store. Stipulator classifies each attachment against the current
corpus and unfiltered binding surfaces as `current` when its requirement is a
member of the named surface, `stale` when the requirement exists but that
relationship does not, or `orphaned` when the requirement does not exist.
Producer claims about source or measurement freshness are opaque payload, not
part of Stipulator's correlation judgment.

**REQ-advisory-non-evidence** (invariant): An advisory attachment MUST NOT
enter verification, satisfy an evidence policy, change a coverage bucket,
resolve a gap, or affect the gate verdict. Evidence that can gate is produced
only by a trusted backend Stipulator invokes in the current verification run;
moving that backend behind a process boundary does not make its output an
advisory attachment.

**REQ-advisory-context** (behavior): Context dossiers MUST render every
explicitly supplied attachment associated with a requested requirement,
including its correlation state, kind, producer, summary, and opaque payload.
No attachment data appears in verification or coverage reports.
