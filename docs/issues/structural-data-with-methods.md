# A structural assertion for a data type carrying an exact method set

`structural.ExportedData` admits a type with no methods. A data type
that owns a published record form carries encoders — gofresh's
`Fingerprint` carries MarshalJSON, UnmarshalJSON, and Validate, and
this repository's witness store now holds that type as its record's
fingerprint (chunk 272) — so its shape is pinned by reflection in
gofresh (TestFingerprintDataShape: fields, types, tags, the exact
method sets on the value and the pointer) rather than by the
structural kind. A `structural.DataWithMethods(t, T, methods...)`
assertion — exported fields only, no unexported ones, and exactly the
named methods — is the kind such a type wants, here and for any
consumer pinning a record form it does not own.

Lands: cross-tool train chunk 228 (stipulator's test-surface band).
