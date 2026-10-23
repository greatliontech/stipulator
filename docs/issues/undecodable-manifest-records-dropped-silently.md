# Rejected runtime records lack a diagnostic account

Lands: tooling-redesign 5.2e

`internal/witnesscache/witnesscache.go`'s `loadEntry` returns no record when
`validFingerprint` rejects a runtime manifest through `runtimeinput.Describe`.
The loader omits that variant without retaining a reason for the subject's
re-execution. The ledger reference is preserved when its coordinate can be
read; this is not a request to erase that protection.

Strict whole-envelope admission, native fingerprint fields and resolution
cache format 5's complete source certificate are implemented. They do not
provide an account of rejected runtime witness records. Keep malformed
records non-authoritative, and retain a bounded, attributed refusal account
without placing their passed outcomes in the flip index or trusting partially
decoded evidence. Do not hand a malformed record to serving merely to obtain
an error string. Pin a rejected variant beside a valid sibling through restart
and re-execution on both faces.

Secondary constituent: 9.2 settles structural-history compatibility, separate
from the load-account obligation. Structural readability and current proof
admission remain distinct under
[REQ-evidence-witness-cache-format-validation](../specs/evidence.md): a readable
historical proof supplies no current reuse authority.
