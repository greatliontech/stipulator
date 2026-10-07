# The transitive seeding walk does not reach dependency and interface routes to a driver

Serving consults a transitive seeding class (REQ-evidence-witness-
freshness): the static in-module callees a bound body resolves through
the type information — called, or named as a value the body may call —
walked to the first helper whose own body drives a run-time-seeded
runner. Two routes to a driver stay outside the walk and serve as
example witnesses:

- a driver reached only through a dependency's helper (a shared
  property-helper module) — the walk stops where the module does;
- an interface method dispatch whose implementation drives the runner.

Each is a served-flake shape the spec states as outside the walk. (The
function-value route closed statically: a function the body names as a
value is walked as a callee, over-approximating reach — fail-closed,
never a serve; a value that reaches the body from elsewhere — a
parameter, a field, a dependency's value — stays outside, since no
declaration names it in the body. The interface route had refused
rather than served before 56e097d; it serves as stated.)
Closing the two needs a reachability judgment beyond declarations —
gofresh's closure over the subject's reachable functions answers both,
at the cost of a proof-level dependency in the classifier.

Lands: gofresh exports a query answering whether a subject's observed
closure reaches a named function (the dynamic-state tier's home,
decided at gofresh chunk 202).
