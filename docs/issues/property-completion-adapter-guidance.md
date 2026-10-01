# Shared property-completion guards need direct-driver guidance

Lands: user decision — retain direct-call classification and document a
guarded-test-context pattern, or design an explicitly reviewed
callback-forwarding adapter form that can establish driver quantification.

Field report: bldc, 2026-10-02, while adding full-depth evidence for Rapid
v1.3.0. That version may report success after fewer than the requested
generated checks as a test deadline approaches. A shared helper wrapped
`rapid.Check` to verify the actual generation receipt and fail closed.

Replacing a test's direct `rapid.Check(t, prop)` with
`property.Check(t, prop)` made existing property witnesses classify as
examples. The check reported, for example:

```text
classified example: rapid.Check not invoked in the bound body
(reached through github.com/greatliontech/bldc/internal/testutil/property.Check)
```

The callback and its generators/assertions were unchanged. The helper
still invoked the actual driver, and transitive seeding still made the
test uncacheable, but the property evidence tier was lost. This agrees
with the intentional direct-driver rule in `internal/backends/golang/golang.go`
(source inspected at ddf1b05); it is an integration/diagnostic friction
report, not a request to promote arbitrary helper calls to quantified proof.

The caller can retain the direct call instead:

```go
rapid.Check(property.Guard(t), func(rt *rapid.T) {
    // Existing generators and assertions.
})
```

`Guard` returns a forwarding test context and registers a cleanup that
checks Rapid's successful-generation receipt. The caller retains visible
quantification; missing, malformed or incomplete receipts fail the test.
The ordinary Go process timeout remains authoritative. bldc is validating
this form against its coverage gate and real deadline/replay regressions.

Recommendation: document this direct-call-preserving approach in the
classification guidance or near-miss remedy. If adapter recognition is
desired instead, it needs a soundness contract distinguishing a forwarded
property callback from a helper that merely runs unrelated randomized work.
