# Historical proof readability conflicts with load-time strategy admission

Lands: tooling-redesign 5.2e

`docs/specs/evidence.md:851–858`,
REQ-evidence-witness-cache-format-validation, promises that cache decoding
checks canonical structure and disposition consistency only: a structurally
valid but incompatible historical proof remains readable, while the current
Gofresh view's `CheckObserved` decides compatibility and grants no reuse.

The source has a stronger load gate. In
`internal/witnesscache/witnesscache.go`, `loadEntry` calls `validFingerprint`,
which calls `validObservation`; line 719 requires
`f.ObservationProof.Strategy == gofresh.ObservationRTA`. A non-current
strategy therefore makes this otherwise structurally admissible variant
absent before a current-view compatibility judgment.

The pinned native codec does not make that comparison unavoidable. Gofresh
v0.116.3's `fingerprint_record.go` decodes the proof's strategy as a string,
checks its structural disposition and canonical re-encoding, and preserves
that strategy. Its `fingerprint_record_test.go` explicitly round-trips a
historical `gofresh/observation-rta@43` proof without inventing dependencies.
Current-view proof admission in `view.go` separately requires the current
strategy and source-bound support. Thus this is a concrete contract/source
discrepancy at Stipulator's extra load predicate, not a claim that the native
codec itself rejects all old strategies.

Resolve the discrepancy with a fixture through the complete consumer path:
use the supported witness envelope version, canonical native encoding, a
content-derived matching file name, valid subject/outcomes/manifest and a
non-current proof strategy. Distinguish decoding, store readability and
current-view reuse; retain valid siblings and malformed-proof controls.
Establish the fixture before choosing the implementation correction or a
contract amendment. Do not fabricate current support, upgrade a historical
proof, or broadly admit unsupported envelope versions.

Secondary constituent: 9.2 settles the final native/envelope compatibility
rule. This filing is based on source and contract inspection; it claims no
newly reproduced runtime failure or completed fixture execution.
