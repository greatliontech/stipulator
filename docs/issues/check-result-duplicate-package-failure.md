# A package failure rides the check result twice

Lands: when the check result's wire shape next changes.

A package-level failure appears in the check result both as a typed
`witness_diagnostics` row (test unset; disposition and truncation
intact) and as the same text in `verify.package_failures`. One fact,
one wire home — the typed row is the survivor candidate, the map the
legacy duplicate. Carried over from the resolved
mcp-check-result-too-large report, whose histogram and summary-view
items landed with the response contract.
