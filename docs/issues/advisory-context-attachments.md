# Context dossiers have no extension point for producer-owned material

Lands: when a consumer needs producer-owned material rendered in Stipulator
context dossiers instead of consuming the producer's typed output directly.

Binding surfaces let an external tool correlate its own results with the
current binding graph without making that tool a Stipulator backend. A generic
context attachment could carry a requirement identifier, binding-surface
identifier, namespaced kind, producer, summary, and opaque protobuf payload.
Stipulator could classify the relationship as current, stale, or orphaned and
render it without storing it or allowing it to enter verification, coverage,
gap resolution, or the gate verdict. Attachments would enter only as explicit
operation inputs, never through a discovered or configured source. Unknown
kinds and payload fields would remain renderable, while a malformed document
would be refused as a whole rather than partially read.

No current consumer needs that projection. Gomutant owns a typed findings
document with measurement freshness, survivors, and survivor dispositions;
callers inspect those findings directly and apply acceptance policy outside
Stipulator. Adding an opaque attachment format before another producer needs
co-rendering would duplicate its result contract without carrying acceptance.
