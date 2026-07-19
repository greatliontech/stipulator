# Empty binding-surface reports do not explain missing implementation bindings

Lands: when the binding-surface report diagnostics next change.

## Observed

After authoring only `tests` bindings for a consuming repository, calling
`stipulator_targets` returned:

```
{"format":"stipulator.binding-surfaces/v1","surfaces":[]}
```

That result was structurally correct: binding surfaces are keyed by
implementation bindings. The output did not explain that no `implements` binding
matched the query, so the agent had to infer the missing record class before
adding implementation bindings and retrying.

## Resolution

When a binding-surface request returns zero surfaces, include diagnostics or
summary counts that distinguish at least:

- no implementation bindings matched;
- implementation bindings matched but no associated executable tests/proofs
  matched;
- filters excluded every surface;
- backend resolution failed.

The current empty export can stay machine-valid, but the report should give an
author enough information to repair the binding graph without reverse-engineering
the surface model.
