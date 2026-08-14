# Multiply-selected witnesses carry no per-invocation identity — whole-corpus cache death

## Symptom

On the tugboat corpus (policy: three invocations — `dst` [tags dst,
toolchain go1.26.5-dst.6], `dst-race` [tags dst, race, same
toolchain], `race` [race, host toolchain] — every one selecting
`./...`), every check re-executes every witness:

```
"testsExecuted": 927, "testsServed": 0, "testsUncacheable": 927,
"uncacheableBlockers": [{"exemplar":
  "github.com/greatliontech/tugboat/internal/arch.TestConsensusCoreCustody",
  "reason": "multiply-selected: a record has no per-invocation identity",
  "witnesses": 927}]
```

A scoped `check` (six requirement ids) on a warm tree took >2 min of
pure re-execution; the fresh-witness serving path never engages. The
cache is structurally dead for this corpus, not cold.

## Reading

An untagged test (the arch analyzers, every untagged unit test) is
selected by more than one invocation — `race` selects it, and the
`dst`/`dst-race` invocations' `./...` selection sees it too on their
build view. The witness record apparently keys the test without the
invocation coordinate, so a multiply-selected test's record is
ambiguous evidence — the conservative verdict is uncacheable, and on
a corpus whose every invocation selects `./...` that conservatism is
total: 927/927.

Likely the same family as the shared-view item quantified in
[cerebro-uncacheable-mass-measured](cerebro-uncacheable-mass-measured.md);
filed separately because the reason class here is single and named
("multiply-selected"), the corpus is tugboat (the check loop's cost
is paid on every chunk gate), and the fix wants the record to carry
the (invocation, test) identity — per-invocation witness identity
looks like the record-shape fix, not a classifier tweak.

## Reproduction

Any `stipulator check` against the tugboat corpus
(`~/repos/github.com/greatliontech/tugboat`, policy as above);
observe `testsServed: 0`, `testsUncacheable: N == testsExecuted`,
one blocker row with the multiply-selected reason.

Lands: with the witness-record identity fix (gofresh/stipulator
shared-view family); verified by a tugboat warm-tree check serving a
nonzero witness majority.
