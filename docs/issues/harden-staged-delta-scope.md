# harden cannot explain staged-delta coverage

Lands: when a consuming repo uses harden as the adversarial-loop entry point
for a staged change set.

`harden` answers whether scoped, bound implementation symbols have surviving
mutants. During a staged-delta review, the operator still has to decide by hand
which changed invariants are covered by harden and which need a manual mutation:
newly touched symbols may be unbound, generated artifacts may be outside the
mutated body set, and integration behavior may live at a caller seam rather
than the bound implementation symbol.

Add a staged-delta scope report, for example `harden --staged-diff`, that walks
the staged diff and classifies each touched requirement/symbol surface:

- covered by an existing harden target;
- skipped because no bound implementation resolves;
- skipped because no witness resolves;
- skipped because the witness class is outside the mutator's operator set;
- skipped because the changed file is generated or data-only;
- skipped because the behavior is an integration seam rather than a bound body.

The report is not a gate. Its job is to make the manual tail explicit, so the
operator can run harden first and then mutate only the surfaces the tool says it
cannot reach.
