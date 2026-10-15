# Supersede drops clause scope and deduplicates distinct claims

Lands: tooling-redesign 5.3

`internal/author/dispose.go` constructs successor bindings from the source
requirement, backend, symbol, role and shape pin, without its clause scope.
Its `bindingExists` duplicate guard also omits the clause. A scoped grant
can consequently become a whole-successor grant, or two distinct scoped
claims can collapse. Ordinary binding retargeting already preserves resolved
clause identity; that repair does not cover supersede.

The remaining obligation is to preserve scope when the successor can
represent it and otherwise refuse the carry, naming the claim. Duplicate
detection must include resolved clause identity. State the successor-mapping
rule deliberately in REQ-change-split-merge and pin matching, missing and
colliding successor clauses. The 2026-09-29 derivation remains: a mechanical
operation cannot manufacture whole-requirement evidence by dropping scope.
