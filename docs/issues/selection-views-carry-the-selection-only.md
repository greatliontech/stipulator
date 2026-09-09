# Selection views load under the selection alone, never the invocation's module mode or profile

A witness group's engine builds under buildFlags — the selection, the
module mode, and the PGO profile — so its analysis describes the binary
the witnesses run as. The symbol-resolution views (the child's typed
views and the served form's selection engines) load under
selectionViewFlags: the effective tag set and toolchain, per
REQ-go-build-selections' two dimensions. An invocation declaring
`-mod=mod` or `-mod=vendor` therefore resolves symbols under a package
graph the default mode selects, which can differ from the witnesses'
(a vendor tree absent from the module cache, or the reverse). Whether
the module mode is a third selection dimension is a spec question
(REQ-go-build-selections names the pair; the policy record is the
authority on which selections exist).

Lands: cross-tool train chunk 222 (the stipulator spec chunk decides
the dimension; the view flags follow).
