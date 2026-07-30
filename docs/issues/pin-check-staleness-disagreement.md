# Argless pin answers "all pins current" while differing content pins await re-consent

Lands: when the blanket pin's response names the differing content pins it
deliberately preserved, and the MCP tool description states the blanket/--req
split.

## Observed, then resolved to a narrower finding

In a consuming corpus (`candosa/cerebro`), requirement-text amendments were
followed by argless `pin` calls that answered `all pins current`; a later
`check` flagged those requirements' content pins as stale, needing
`pin --req <id>`. The CLI help settles the split as design: the blanket form
backfills unset content pins and refreshes shape pins but **never rewrites a
differing content pin** — «staleness cannot be laundered» — and `--req` is the
editorial re-consent. Running `pin --req` for the two requirements re-pinned
them as documented. No staleness-detection defect exists.

## What remains

1. **The blanket response conceals the protected pins.** `all pins current` is
   the answer a caller reads as "nothing awaits me," when the truthful state
   was "two content pins differ and await editorial re-consent via --req."
   The anti-laundering refusal is right; the response should surface what it
   refused to launder — a line naming the differing requirements — so the
   caller learns about the pending re-consent from `pin` itself rather than
   from a later `check`.
2. **The MCP tool description oversimplifies.** It reads «pin (re-consent
   after spec edits)», which describes exactly the case the blanket form
   deliberately does not perform; an MCP caller has no `--help` in front of
   them and acts on the docstring. The description should state the split:
   blanket = backfill/refresh, never rewriting a differing pin; named
   requirements = editorial re-consent. (Whether the MCP surface accepts a
   requirement argument at all is part of the same gap.)
