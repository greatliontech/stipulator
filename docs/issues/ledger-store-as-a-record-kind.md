# The compartment ledger store is a record kind implemented by hand

The witness cache's ledger sub-store (`internal/witnesscache`: the
`ledgers/` directory, `installLedger`, `readLedger`, `sweepLedgers`)
carries its own dot-prefixed temporary, its own name-content agreement
check (the digest repeated inside the file), and its own sweep with a
since-sparing rule, beside the record store core
(`internal/recordstore`) every other kind composes. Its naming differs
from the core's — a ledger is named by its compartment digest alone,
with no fingerprint segment — so folding it is a design choice: either
the core's `Name` grows a one-segment form and the ledger store becomes
a nested `recordstore.Store`, or the ledger's naming stays its own and
the fold is refused as a coarsening. What merges: the temporary, the
scan, the atomic write, the sweep walk; what stays: the digest-only
name and the concurrent-install sparing rule (a ledger younger than
the load is never swept).

Lands: cross-tool train chunk 185
