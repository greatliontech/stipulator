# harden cannot attack analyzer witnesses — no structural mutation class

Lands: when an analyzer witness needs adequacy evidence.

`harden` mutates implementing function bodies (the go/2 operator set) reached through
role-IMPLEMENTS bindings. A structural clause has no implementing symbol — its
"implementation" is a property of the module's import graph or type relations — so its
analyzer witness never gets a teeth check: harden has nothing to mutate, and a vacuous
witness passes forever.

The vacuity modes are real. `structural.NoImport` guards one of them itself (a
from-pattern matching zero packages fails loudly), but nothing guards the others: a
forbidden path gone stale after a package rename (NoImport constrains the *from* side
only), or an Implements assertion against a type that no production code constructs.

The mutation class analyzer witnesses need is structural, not expression-level:
synthesize the forbidden state in a scratch copy of the tree and require the bound
witness to fail —

- for `NoImport`: inject a blank import of the forbidden package into a package matched
  by the from-pattern; the witness must fail naming a chain;
- for `Implements`: remove or break the asserted method set; the witness must fail.

A consuming corpus ran exactly this loop by hand — blank imports of the forbidden tier
injected into three matched packages, the witness required to name the full chain each
time, then restored. That is the mechanical break-observe-restore cycle harden exists to
own, applied one level up.

Trust interplay: a structural-mutation kill is evidence about the *witness*'s teeth, not
about the prover's soundness — prover-trust-tiers governs the latter and is untouched by
this. Adjacent: witness-subset-adequacy (per-requirement adequacy probes) would subsume
part of this if it lands first with a structural mutant source.
