# Standard-library testing/quick is not recognized as a property driver

Lands: when the Go property-driver classifier or its seeding analysis is next changed.

## Consumer evidence

Weaver's invariant adoption binds
`internal/state.TestWaitOutcomeHistoryProperty`, which directly calls
`testing/quick.Check` over a generated attempt count and two booleans, checking
wait outcome and causal position. The accepted offline race policy runs it
successfully, but the coverage result classifies it as example evidence with
`no property driver or analyzer call in the bound body`.

Other existing consumer tests use the same standard-library driver for inbox
folding/recipient integrity, artifact paging, and operation settlement. They are
not wrappers around Rapid and are not merely named property tests without a driver.
The current Go backend recognizes other drivers but not `testing/quick`.

A minimal public shape is:

```go
func TestIdentityProperty(t *testing.T) {
    if err := quick.Check(func(x uint64) bool {
        return Identity(x) == x
    }, nil); err != nil {
        t.Fatal(err)
    }
}
```

Bind that function as a test for an invariant; the direct standard-library driver
does not confer the property witness tier. This is a support/diagnostic gap, not
evidence that every helper property proves its surrounding integration contract.

## Required derivation

Define the supported direct-driver forms (`Check`, and whether `CheckEqual` is
included), callback quantification, and random-seed/configuration semantics before
classifying them. Apply the same freshness/seeding discipline as other runtime-
seeded properties; recognizing a new driver must not make randomized witnesses
unsafely reusable. Distinguish driver recognition from the author/reviewer's
responsibility for assertion breadth and non-vacuity.

If these forms remain unsupported, name the unsupported driver in the diagnostic
rather than imply no property driver was called, and document the supported
alternatives. Do not require consumers to turn existing properties into artificial
fuzz wrappers merely to obtain a class label.

Until support exists, Weaver retains its invariant statements and passing example/
property tests with explicit uncovered-evidence gaps. It does not assert purity,
downgrade the requirements, or claim complete invariant coverage.
