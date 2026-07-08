# harden lacks integration and generator mutation recipes

Lands: when a corpus repeatedly needs manual mutants for generated data,
resolver seams, or caller mappings.

The body mutator covers expression-level implementation changes. Some recurring
adequacy checks are recipe-shaped instead:

- mutate one generated value while leaving the source corpus unchanged, proving
  a drift guard notices;
- drop or invert a caller-side fail-closed mapping, proving the integration seam
  surfaces the intended unresolved state;
- remove a parser guard for a required row cell, proving malformed corpus input
  fails closed;
- change a resolver precedence edge, proving the composed result selects the
  legally stronger or more specific source.

These recipes are not arbitrary fuzzing. They are named mutation classes over
repo shapes harden can identify: generated output paired with source input,
parser/compile guards paired with diagnostics tests, and resolver/caller seams
paired with behavior witnesses.

Add opt-in recipe support so projects can declare these mutation classes near
their bindings or harden config. A recipe should still report survivors as
findings and should say which requirement or invariant the recipe was intended
to attack.
