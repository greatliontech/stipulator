# Record verbs assume a single writer

Lands: when concurrent agents write records in one working tree.

Every record verb (bind, unbind, gap, attest, dispositions) loads the
store, computes an update, and returns file contents the caller writes —
last writer wins. With one writer per tree (the current mode) git review
is the serialization point and history is the audit log, by design
(REQ-change-transient; REQ-core-scope). Two agents writing concurrently
can silently drop each other's records between load and write.

If that mode arrives, the fix is compare-and-swap at the verb layer —
the update carries the digest of the record file it read, and the write
refuses when the file moved — not actor/approval metadata in records,
which REQ-core-scope forbids; identity and authorization stay with the
transport (git, MCP session), never in the store.
