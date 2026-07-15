# Go module rename lacks stored-symbol migration

Lands: before a corpus with stored Go symbol references changes module path, or when the binding
rewrite surface next changes.

## Observed

Renaming a Go module changes the import-path prefix of every stored Go binding symbol under that
module. Verification correctly reports the old symbols as broken, but no validated bulk migration
command or suggested remediation exists. `bind` and `unbind` operate per requirement and symbol,
`pin` refreshes pins rather than identities, and spec dispositions retarget requirement identities
rather than implementation symbols.

In one consuming corpus, a module rename invalidated 491 stored bindings. Direct text replacement
repaired the prefix, after which `stipulator pin` refreshed the derived shape pins, but that route
bypasses backend resolution, collision checks, record ownership rules, and an auditable old-to-new
report.

## Resolution

Provide a validated symbol-retarget operation for an exact backend and prefix mapping. It must
preview affected records, resolve every replacement, reject collisions or partial rewrites
atomically, update every applicable Stipulator-owned binding record, refresh derived pins where
required, and report old and new identities. Producer-owned findings remain outside that rewrite.
Until then, broken-symbol diagnostics should state the supported remediation.
