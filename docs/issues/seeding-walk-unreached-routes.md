# The transitive seeding walk does not reach dynamic and dependency routes to a driver

Serving consults a transitive seeding class (REQ-evidence-witness-
freshness): the static in-module callees a bound body resolves through
the type information, walked to the first helper whose own body drives
a run-time-seeded runner. Three routes to a driver stay outside the
walk and serve as example witnesses:

- a driver reached only through a dependency's helper (a shared
  property-helper module) — the walk stops where the module does;
- a function value — a helper stored in a variable and called, or
  passed to `t.Run(name, helper)` and called by the harness;
- an interface method dispatch whose implementation drives the runner.

Each is a served-flake shape the spec states as outside the walk. (The
interface route had in fact been refusing rather than serving — the
collector appended the abstract method and the declaration lookup
refused it; corrected so the route serves as stated, with a type
parameter's method refused as the clause's default.)
Closing them needs a reachability judgment beyond declarations —
gofresh's closure over the subject's reachable functions answers all
three, at the cost of a proof-level dependency in the classifier.

Lands: a field report of a served flake through one of these routes,
or the next change to the seeding walk's callee resolution.
