# Go backend

The Go backend verifies bindings against Go source: symbol resolution and
shape hashing through the type checker, structural proofs through analyzers,
and witnesses through the test runner. It is a plugin behind the backend
interface (REQ-backend-surfaces); nothing in the core knows Go exists.

## Symbols and shapes

**REQ-go-symbol** (behavior): A Go symbol reference MUST name the package
import path, the identifier, and the receiver type for methods; the object's
kind and shape are resolved from the code, never declared in the reference,
so they cannot diverge from reality.

**REQ-go-shape-hash** (wire): The Go shape hash MUST be computed over the
object's declaration as rendered by the Go type-checker's object printer
with full package-path qualifiers, per REQ-model-hash-canonical-form; the
rendering is toolchain-versioned, so a toolchain change may re-stale shape
pins, restored by re-pinning.

**REQ-go-static-binding** (behavior): Static binding verification MUST
resolve the symbol through the Go type checker and compare shape hashes; a
package load failure is a verification error, not an absence.

## Witnesses

**REQ-go-witness** (behavior): Witnesses MUST be derived from `go test -json`
output produced in the current verification run, correlating passed tests
with bindings of role `tests`; toolchain cache replays are current-run
equivalent (the cache key is the tree content), and a bound test producing
no outcome in a witnessed run is unwitnessed and reads as `broken`; a
skipped test grants no witness without reading as `broken`.

**REQ-go-witness-class** (behavior): A witness MUST be classified `property`
when its bound test is a fuzz target (a function taking `*testing.F`), and
`example` otherwise; the classification is resolved from the code, never
declared.

**REQ-go-race** (behavior): Witness runs MUST enable the race detector, so
every witness is race-attributed.

**REQ-go-fuzz-exploration** (behavior): A fuzzing campaign MUST NOT feed the
gate directly — campaigns are time-bounded and nondeterministic; their
counterexamples enter the committed seed corpus, whose deterministic replay
in ordinary test runs is the witness.

**REQ-go-covers** (behavior): Tests MAY register requirement coverage at
runtime through the provided `Covers(t, id)` helper, which yields
subtest-granular witnesses in the same run.

**REQ-go-covers-crosscheck** (behavior): A runtime registration naming a
requirement that has no matching binding of role `tests` MUST be a
verification error; the binding store is the only claim source.

## Structural provers

**REQ-go-structural-provers** (behavior): The Go backend MUST provide
analyzers asserting import constraints (a package set does not import
another) and interface satisfaction (a named type implements a named
interface), as the initial prover set for `structural` clauses.

## Slice

**REQ-go-slice** (behavior): Given symbol references, the Go backend MUST
return the declarations of their transitive dependency frontier —
signatures and named types declared within the module, rendered by the
object printer and shape-pinned, canonically ordered — returning facts
only: no depth budgets, no exemplar selection, no rendering policy.

## Generated code

**REQ-go-generated-detect** (behavior): Generated Go files MUST be detected
by the standard `^// Code generated .* DO NOT EDIT\.$` marker, feeding
REQ-evidence-generated-code.
