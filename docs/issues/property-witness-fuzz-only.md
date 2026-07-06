# Property-witness classification recognizes only native fuzz targets, not property libraries

Lands: when the witness classifier recognizes common Go property libraries (rapid, gopter), or
the fuzz-only rule is documented as the way to author a property witness.

An `invariant` requirement's minimum evidence is a **property witness** — "a generator-driven
test quantifying over inputs" (`docs/specs/evidence.md` REQ-evidence-ladder). But the go
backend classifies a test as a property witness *iff* it is a native Go fuzz target:

> `golang.go` `WitnessClass`: a fuzz target — a function taking `*testing.F` — yields a
> property witness; everything else [yields an example witness].

So a generator-driven test written with `rapid.Check` (or gopter) inside a `func Test…(t
*testing.T)` is classified as an **example** witness, which does not meet an invariant's
minimum. A project standardized on rapid cannot cover its invariants without rewriting those
tests as `func Fuzz…(f *testing.F)`.

Probe: binding an example test to an invariant leaves it uncovered —

```
stipulator bind --req REQ-corpus-image-total --role tests --symbol …corpus.TestCompileAEATDeadlines
stipulator gate
# -> uncovered: REQ-corpus-image-total (needs a property witness or analyzer proof (invariant))
```

Concretely: cerebro's `TestPropTotalityAndDeterminism` drives `rapid.Check` over generated
inputs — a genuine property witness by the ladder's definition — but would be scored as an
example witness because it takes `*testing.T`, not `*testing.F`.

Options:
- Broaden the classifier to detect generator-driven `*testing.T` tests — e.g. a `rapid.Check`/
  `rapid.MakeCheck` call or a `pgregory.net/rapid` / `gopter` import in the test body — and
  score those as property witnesses.
- Or document that, for the go backend, a property witness must be a native fuzz target, so
  authors targeting invariant coverage write `Fuzz…(f *testing.F)` deliberately rather than
  discovering the rule from an uncovered gate.
