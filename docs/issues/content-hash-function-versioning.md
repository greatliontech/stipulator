# A tool rebuild moves content hashes, and a re-consent over unchanged text is indistinguishable from an amendment

Consumer report (bldc, 2026-08-30): a `stipulator` rebuild mid-session
moved the computed content hash of exactly four requirements with
`docs/specs/` byte-identical to a commit whose `check` had just
passed — the same staged tree went from pass to "4 stale content
pins" across the rebuild; restoring the tree changed nothing, which
attributed it to the hashing. The prescribed remedy (`pin --req` per
requirement) is faithful — the text under consent is unchanged — but
it cannot be SEEN to be faithful: the blanket form's safeguard
assumes a differing content pin means the text moved, so when the
hash function moves instead, a re-consent over identical text is
recorded exactly like a consent to an amendment. An auditor reading
the binding diff sees four requirements re-consented and cannot tell
which happened. Only 4 of ~200 requirements moved, so the
normalization change was narrow — a tree that never re-runs a full
check would not notice at all.

Asks, either sufficient: version the content-hash function and treat
a function-version change as its own state ("rehash", re-pinnable in
bulk without editorial consent); or have `pin --req` record the
declaring document's blob hash beside the content hash, so a
re-consent over unchanged text is self-evidently that.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
