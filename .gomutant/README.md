# Outcome adapter mutation inventory

`outcome-targets.json` selects the package-process outcome-preparation boundary
and its explicit witness. It is a scoped inventory, not whole-repository coverage.

```sh
mlock run gomutant run --targets .gomutant/outcome-targets.json --budget 0
```

Executed kills, equivalence judgments and record freshness are separate evidence.
The harness creates scratch directories and performs startup cleanup; its runtime
inputs can prevent reusable campaign evidence. A non-reusable finding still
reports measured outcomes, but its refusal never establishes equivalence for an
unjudged survivor. Committed ephemeral judgments retain their stated source and
dependency assumptions.
