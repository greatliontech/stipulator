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

**REQ-advisory-targets** (wire): The `targets` operation MUST derive one
backend-independent binding surface for every unique implementation backend
and symbol named by an in-corpus `implements` binding. A surface carries the
set of in-corpus requirements implemented by that pair and the set of unique
`tests` and `proves` binding triples `(role, backend, symbol)` associated with
those requirements. Binding file layout, record order, duplicate associations
from distinct claims that project to an already represented requirement or
triple, content and shape pins, source state, backend availability, and
verification results do not change the surface. The protobuf wire form uses
these fields:

| Message | Field | Number | Type |
|---|---|---:|---|
| `BindingSurfaceReport` | `surfaces` | 1 | repeated `BindingSurface` |
| `BindingSurfaceReport` | `format` | 2 | string |
| `BindingSurface` | `id` | 1 | string |
| `BindingSurface` | `backend` | 2 | string |
| `BindingSurface` | `symbol` | 3 | string |
| `BindingSurface` | `requirement_ids` | 4 | repeated string |
| `BindingSurface` | `bindings` | 5 | repeated `SurfaceBinding` |
| `SurfaceBinding` | `backend` | 1 | string |
| `SurfaceBinding` | `role` | 2 | `BindingRole` |
| `SurfaceBinding` | `symbol` | 3 | string |

Every report sets `format` to `stipulator.binding-surfaces/v1`. Surfaces are
ordered by implementation backend then symbol; requirement identifiers are
ordered by identifier; associated bindings are ordered by role (`tests`
before `proves`), backend, then symbol. All string ordering is lexicographic
over UTF-8 bytes. Repeated collections contain no duplicate semantic element.

**REQ-advisory-validation** (behavior): Surface derivation MUST refuse an
ill-formed binding store without returning a partial report. Ill-formed means
a malformed record file, an exact duplicate binding claim, a missing
requirement identifier, backend, role, or symbol, an unknown requirement, or
an unrecognized role. Unset or mismatched pins, an unavailable backend, an
unresolved or generated symbol, and a failed or absent witness are verification
state rather than graph structure and remain represented.

**REQ-advisory-filtering** (behavior): The `targets` operation MUST accept
exact-match filters for implementing requirement identifiers, implementation
backends, and implementation symbols. Values within one populated dimension
are alternatives and populated dimensions intersect. A requirement filter
matches a surface containing any named implementing requirement. Filtering is
applied after complete unfiltered derivation, so an included surface and its
identifier are unchanged. A supplied filter set matching no surface is
refused; an unfiltered corpus with no implementation bindings returns a valid
empty report.

**REQ-advisory-output** (wire): Every successful CLI `targets` invocation MUST
render the `BindingSurfaceReport` as standard ProtoJSON with symbolic enum
names and unpopulated repeated fields emitted as empty arrays. Inline output
ends in one newline. File output is rendered completely before atomically
replacing the destination and carries no human summary in the file.

**REQ-advisory-surface-id** (wire): A binding-surface identifier MUST be the
REQ-model-hash-func digest of the canonical bytes below. `str(x)` is the
shortest base-10 ASCII rendering of the UTF-8 byte length of `x`, one colon,
then those UTF-8 bytes. `set(xs)` is the shortest base-10 ASCII rendering of
the element count, one colon, then each already-canonical element. Zero is
rendered `0`; every other decimal rendering has no leading zero. The preimage
is, in order:

1. `str("stipulator-binding-surface-v1")`;
2. `str(implementation backend)`;
3. `str(implementation symbol)`;
4. `set(sorted unique requirement identifiers)`, each element encoded by `str`;
5. `set(sorted unique associated bindings)`, each element encoded as
   `str(role token)`, `str(binding backend)`, `str(binding symbol)`.

The stable role tokens are `tests` and `proves`, in that order. The SHA-256
function consumes these bytes directly: no Unicode normalization, whitespace
folding, serialized protobuf bytes, separator, or terminator contributes.
