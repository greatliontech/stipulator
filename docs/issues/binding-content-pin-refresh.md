# A stale content pin has no refresh verb — rewords force unbind + rebind

Lands: when the bind/pin record verbs are next touched.

Rewording a requirement invalidates the `content_hash` every binding of that requirement
pins — by design: the pin is the consent trail, and `verify` correctly reports
`contentPinned: false`. But the recovery flow is broken end to end:

- `pin` (MCP) accepts no arguments, returns `{}`, and writes nothing — a silent no-op.
  Passing a requirement id is schema-rejected (`unexpected additional properties`).
- Re-running `bind` with the same (requirement, symbol, role) refuses with
  `identical binding already exists` — the identity check ignores `content_hash`, which is
  exactly the field that needs refreshing. The stored binding is *not* identical to what a
  fresh bind would write.
- The only working path is `unbind` + `bind`, two verbs to express one intent.

Observed in a consuming corpus after a mid-change-set clause reword; the stale pin was
found only because `verify` was run for an unrelated reason.

Fix either verb, not both:

- `bind` on an existing (requirement, symbol, role) whose stored hash is stale refreshes
  the pin — re-binding *is* re-consent to the current clause text; or
- `pin` takes requirement ids (mirroring `read_spec`/`context`) and refreshes the hashes of
  their bindings, reporting exactly what it re-pinned.

Either way: no verb should return an empty object after changing nothing — a no-op says
what it skipped and why.
