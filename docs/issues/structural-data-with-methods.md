# A structural assertion for data carrying an exact method set

Lands: tooling-redesign 5.6

`stipulate/structural`'s `ExportedData` admits a type with no methods.
Gofresh's `Fingerprint` owns MarshalJSON, UnmarshalJSON and Validate beside
its exported fields; `internal/witnesscache` aliases that native type rather
than maintaining a mirror. Gofresh's `TestFingerprintDataShape` therefore
uses reflection to pin fields, types, tags and exact value/pointer method
sets. The original need was identified during native record-form adoption.

Provide an exact-method-set structural assertion if it can express those
same obligations without weakening `ExportedData`. Refuse extra hidden state
and extra or missing methods; distinguish value and pointer method sets.
Keep native codec ownership and encoder-error propagation intact. A shape
assertion proves neither encoding semantics nor historical compatibility;
secondary constituent 9.2 retains those separate obligations.
