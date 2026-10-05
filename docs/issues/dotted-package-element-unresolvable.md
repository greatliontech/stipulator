# A package whose last path element carries a dot never resolves

The symbol grammar every binding, gap and claim spells — `import/path.Symbol`
— puts the package boundary at the first dot after the last slash
(`packageOf`), so a package whose last path element carries a dot
(`gopkg.in/x.v3`, `example.com/dotted/m.v2`) is split wrong at every
site that derives a package from a symbol: the resolver child's load
scope (`childPatterns`), the capture's subjects (`captureUnder`) and
the publish's subjects. Such a symbol loads under a package that does
not exist, resolves `NotFound` with the witness class refusal "not a
runnable test witness", and never captures or publishes. Found at chunk
307's review (2026-10-05, the reviewer's M2): the publish account first
named it "without a closing capture" — a wrong cause for a symbol the
child never resolved.

Reproducer (the pin written and withdrawn at 307.D — it fails at the
first ask, before any publish):

```go
dir := writeModule(t, map[string]string{
	"go.mod":         "module example.com/dotted\n\ngo 1.26\n",
	"m.v2/m.go":      "package m\n\nfunc F(n int) int { return n + 1 }\n",
	"m.v2/m_test.go": "package m\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { _ = F(1) }\n",
	".stipulator/policy.textproto": "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
})
s, _ := NewServed(ctx, dir, []string{"example.com/dotted/m.v2.F"})
got := ask(t, s, "example.com/dotted/m.v2.F") // res: NotFound, want Resolved
```

The fix derives: the package is the longest import path the module's
package list declares that the symbol extends with `.` — unambiguous
whenever the module declares the package at all (`m` and `m.v2` both
declared still resolve: `m.v2.F` extends `m.v2`, the longer) and
non-breaking (no binding re-keyed); one derivation shared by the child's
load scope, the capture and the publish. Re-keying every binding with a
delimiter is fleet-wide churn for the same answer and is refused.

The grammar is the fleet's: gomutant and gofresh spell symbols the same
way; whether their parsers split at the same dot is checked at 249's
open, and a sibling filing follows wherever one does.

Lands: cross-tool train chunk 249 (the witness pipeline's monoliths —
the resolver's load scope and the one symbol-to-package derivation).
