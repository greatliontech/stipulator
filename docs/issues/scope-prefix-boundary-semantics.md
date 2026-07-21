# Scope matching is raw-prefix, not element-bounded, and ids scopes drop package-less diagnostics

Lands: when scope matching semantics are next deliberately changed.

Two related looseness classes in view scoping, both over-inclusion or
visibility-narrowing only — never a verdict change (the gate verdict
stays global under every scope):

1. **Raw prefix, no element boundary.** `Scope.keeps` prefix-matches
   docs and symbols without an element-boundary check, and the bindings
   view's diagnostic clause deliberately mirrors it for coherence:
   `Path: "example.com/p"` keeps `example.com/p2` rows and diagnostics
   alike, and `docs/spec` matches `docs/specs.md`. Fixing one matcher
   piecemeal would show a package's rows while suppressing its
   diagnostics (or vice versa); an element-boundary rule must land
   across doc, symbol, and diagnostic matching in one change.

2. **Ids/glob/bucket scopes cannot rescue package-less diagnostics.**
   A path scope keeps a build-broken package's diagnostic directly by
   path match even though the broken package's rows resolve to no
   package. A Path-empty scope (ids, filter, bucket) has no path to
   match, so the same broken package's diagnostic drops from the scoped
   view while its Broken row stays. Mitigations: the unscoped bindings
   view, the verify summary's failure headings, and check-level
   diagnostic rows all still carry it.
