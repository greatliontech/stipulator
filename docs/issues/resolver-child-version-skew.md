# The resolver child is self-executed lazily with no version handshake

The served backend's resolver client resolves its own executable path
at construction and spawns the child on the first typed question. A
long-lived parent — the MCP server is the canonical one — whose binary
is replaced on disk between construction and that first question
self-executes a different build. The handshake exchanges readiness and
an error only; the JSON-lines exchange decodes with unknown fields
ignored and missing ones zero-filled, so a renamed or added response
field reads as a legitimate absent answer (a symbol not found, an
empty class) rather than a refusal. The fix is a protocol identity in
the handshake — the parent's build identity, refused on mismatch with
a reason naming both — or an eager spawn at construction.

Lands: cross-tool train chunk 224 (the next stipulator code chunk).
