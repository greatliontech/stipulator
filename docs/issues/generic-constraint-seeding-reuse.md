# Generic constraint-method seeding refusals dominate Cerebro witness reuse

Cerebro reports thousands of uncacheable witnesses because generic constraint
method calls resolve to no declaration in the seeding walk. The concrete methods
exist. This is a conservative analysis limitation, not evidence of a missing Go
symbol or a failing test; investigate a sound precision improvement and the cost
of repeated closure walks without forcing unsafe serving.

Lands: the next Go seeding-analysis slice that exercises a generic constraint
method with both deterministic and runtime-seeded implementations; coordinate
with `seeding-walk-unreached-routes`, preserving fail-closed classification for
unresolved targets before admitting any new reuse.

## Field evidence and version boundary

The completed Cerebro check based on `eff4878c0` plus an uncommitted modelo130
repair ran with Stipulator v0.73.6 / gofresh v0.112.1. It reported 8,321 executed
witnesses, 8,304 uncacheable. The displayed histogram included:

| Witnesses | Reported unresolved route |
|---:|---|
| 2,857 | `Accepts` in `machinery/primitives/temporal.NewSeries` |
| 562 | `Name` in `(*internal/books.producerBound[K, In]).constructedFor` |
| 219 | `Accepts` in `machinery/primitives/temporal.NewRegistry` |
| 218 | `moduleNumber` in `subjectfacts.SealModuleList` |

These four displayed reason buckets account for 3,856 witnesses. This is not a
count of every generic-related refusal or a prediction of the number that would
become cacheable: other independent reasons may surface after one is resolved.
The histogram also contained random-seeded properties (intentionally executed
every time), static dynamic-state refusals and out-of-bracket input reads.

The newer c9ea6cd build's one-requirement check did not reach execution within
ten minutes; no comparable current-build runtime histogram was obtained. However,
inspection at `c9ea6cd24f0ed51e3e5d2db3efe71b60c214d531` finds the deliberate
constraint-method refusal in `internal/backends/golang/golang.go` near lines
1094–1165, and its regression distinction in `seeding_test.go` near 486–511.
Ordinary instantiated methods use `Origin()`; this is not a claim that all generic
method resolution fails. See [the discovery field report](cerebro-scoped-discovery-cost.md)
for executable identity, timings and the PATH-version trap.

## Minimal shape to investigate

This is a reduction sketch, **not an executed standalone Stipulator reproducer**:

```go
type admissible interface { Accepts(int) bool }
type deterministic struct{}
func (deterministic) Accepts(n int) bool { return n >= 0 }
func admits[T admissible](v T, n int) bool { return v.Accepts(n) }
func TestAdmission(t *testing.T) {
    if !admits(deterministic{}, 1) { t.Fatal("positive value refused") }
}
```

The real `temporal.NewSeries` path declares concrete `Accepts` methods in
Cerebro's `machinery/primitives/temporal/series.go` and calls the constrained
method from generic code. The diagnostic's “no declaration” describes the
analysis boundary, not absent implementation. Reduce that observed path to a
bound witness under an accepted policy and retain its `explain --witness` chain.

Pair any deterministic fixture with a concrete implementation that reaches a
runtime-seeded driver, and with unresolved/multiple implementations. Do not
globally whitelist `Accepts`, `Name`, generic methods or the containing packages.
Do not make every generic caller cacheable merely because one concrete call
site has a deterministic implementation.

## Precision and performance acceptance

- Preserve classification for actual runtime-seeded and unclassifiable paths.
  If specialization can prove a target set, follow every reachable target and
  its governing inputs; retain refusal where it cannot.
- Separate classification precision from performance: per-function memoization
  already exists, while a fresh breadth-first traversal/seen set is used per
  witness (`golang.go` near 1167–1180 and 1251–1343). Repeated traversal is a
  mechanism to measure, not a proven hotspot or justification for unsound reuse.
- Record baseline/current binary identity, policy/build selection, witness set,
  reason transitions and measured cold/warm cost. Count newly served witnesses
  only after an actual second run proves legitimate reuse.
- Explain remaining independent refusals. Cerebro's missing `acceptance`
  observation bracket is repository policy work; dynamic-state messages require
  their own `explain` and are not evidence that mutable runtime state was observed.

The older dependency/interface-driver issue concerns routes that can be served
as examples without reaching a seeded driver. This issue concerns conservative
generic-dispatch refusals that prevent reuse. They may share a reachability
mechanism, but their opposite safety cases must both survive a consolidation.
