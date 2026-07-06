# Runtime registrations are crosschecked and reported, never consumed as evidence

Lands: when coverage evidence derivation is next touched.

REQ-evidence-witness reads "a named test passed in the current
verification run while bound — or registered at runtime — to the
requirement", and REQ-go-covers promises "subtest-granular witnesses in
the same run". The implementation delivers the granularity to the report
only: registrations are crosschecked against the binding store and carried
as `RegistrationResult` with per-subtest outcomes, but coverage evidence
flows exclusively from `BindingResult` outcomes on top-level test symbols.

Concretely: a bound table test where `TestTable/case-a` (registering
REQ-x) passes while sibling `case-b` fails yields `TestTable` failed →
REQ-x reads broken, even though the registration data shows the covering
subtest passed. Defensible conservatism — a failing sibling taints the
binary — but it is the opposite of what the spec sentence promises, and
the discrepancy is invisible because nothing consumes the registration
leg.

First external consumers (cerebro) lean on table-driven tests, which is
exactly where the two readings diverge. Resolution needs a decision, not
just code: either coverage consumes registration outcomes as a witness
source (registration passed → witness granted even when a sibling failed),
or the spec sentences narrow to match the conservative implementation
("registrations refine reporting; evidence follows the bound test's
outcome"). Spec wins by default; this is flagged for the user's call.
