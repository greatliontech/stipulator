# The transitive seeding walk does not reach dependency and interface routes to a driver

Serving consults a transitive seeding class (REQ-evidence-witness-
freshness-seeded): the static in-module callees a bound body resolves through
the type information — called, or named as a value the body may call —
walked to the first helper whose own body drives a run-time-seeded
runner. Two routes to a driver stay outside the walk and serve as
example witnesses:

- a driver reached only through a dependency's helper (a shared
  property-helper module) — the walk stops where the module does;
- an interface method dispatch whose implementation drives the runner.

Both are explicitly outside the current contract's declaration walk; this
filing does not classify that intended boundary as a new regression. (The
function-value route closed statically: a function the body names as a
value is walked as a callee, over-approximating reach — fail-closed,
never a serve; a value that reaches the body from elsewhere — a
parameter, a field, a dependency's value — stays outside, since no
declaration names it in the body. The interface route had refused
rather than served before 56e097d; it serves as stated.)
Extending these routes needs a separately justified reachability judgment
beyond declarations and a deliberate contract change. A prospective shared
query must demonstrate the required target set under the complete selection;
an observed closure is not assumed to answer every open dispatch exactly.
The source boundary is `internal/backends/golang/golang.go`'s
`staticCallees`, `bodyFactsOf`, `seededThrough` and `seededInAnyView`.

Lands: tooling-redesign 7.8

Preserve full-inventory requests and cross-view certificates. Pin a dependency
helper and interface target that drive a seeded runner beside deterministic
and unresolved controls before changing reuse. Function declarations named
as values already participate; do not rebuild that completed route.

The [generic constraint-method field report](generic-constraint-seeding-reuse.md)
tracks the converse problem: conservative unresolved-dispatch refusals preventing
reuse. Any shared reachability work must retain both safety cases; an improvement
to one does not establish the other's correctness.
