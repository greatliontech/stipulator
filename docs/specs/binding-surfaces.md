# Binding surfaces

External tools may examine the binding graph without becoming trusted
verifiers. Stipulator exports current binding surfaces as producer-owned
addresses; it neither requests work nor interprets a consumer's method or
results. Trusted evidence remains evidence derived by a backend Stipulator
invokes during the current verification run.

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
but leave an included surface and its identifier unchanged. The protobuf wire
form uses these fields; repeated fields preserve the canonical ordering above:

| Message | Field | Number | Type |
|---|---|---:|---|
| `BindingSurfaceReport` | `surfaces` | 1 | repeated `BindingSurface` |
| `BindingSurface` | `id` | 1 | string |
| `BindingSurface` | `backend` | 2 | string |
| `BindingSurface` | `symbol` | 3 | string |
| `BindingSurface` | `requirement_ids` | 4 | repeated string |
| `BindingSurface` | `bindings` | 5 | repeated `SurfaceBinding` |
| `SurfaceBinding` | `backend` | 1 | string |
| `SurfaceBinding` | `role` | 2 | `BindingRole` |
| `SurfaceBinding` | `symbol` | 3 | string |

**REQ-advisory-surface-id** (wire): A binding-surface identifier MUST be the
REQ-model-hash-func digest of this canonical byte sequence: the ASCII domain
`stipulator-binding-surface-v1`, followed by the implementation backend and
symbol, then sorted requirement identifiers, then evidence bindings sorted by
role, backend, and symbol. Every string is encoded as its base-10 UTF-8 byte
length, one colon, then those bytes; each collection is preceded by its
base-10 element count and one colon. No separators or serialized protobuf
bytes otherwise contribute.
