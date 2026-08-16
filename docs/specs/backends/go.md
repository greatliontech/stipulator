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

**REQ-go-build-selections** (behavior): Symbol resolution MUST span the
accepted policy's build-selection dimensions - one package view per
distinct invocation (effective tag-set, toolchain) pair beside the
default no-tag view, where the effective tag-set is the declared tags
plus the implicit `race` tag of a race-detecting invocation, each view
loading under its own selection's toolchain exactly as its
invocation executes, derived
from the policy record itself (the policy is the authority on which
selections exist; no separate configuration names them) - so a symbol
declared only under a build tag, or under `//go:build race`, resolves,
shape-hashes, and binds
exactly as an untagged one. Execution discovery lists under the same
effective tag-set, so a gated test is selected by exactly the
invocations that would compile it. A declared GOOS/GOARCH equal to the
host is the host view; a cross-platform selection cannot execute
on-host, so it gets no resolution view and is a named refusal beside
the unloadable-view class - a reference the loaded views cannot answer
refuses with the unresolvable selection named, never a silent absence. The default view is consulted first and
then each tagged view in the policy's canonical invocation order; the
first declaring view supplies the resolution and shape hash, and the
declaring view's file is the symbol's file for impact and freshness
keying, so a tag-gated edit invalidates the witnesses it declares. A
tree without a policy record resolves the default view alone; a
malformed record is a verification error, never a silent narrowing to
the default view. A tagged view that cannot load degrades to a named
refusal - binding stays healthy for every symbol the loaded views
resolve, while a reference they cannot answer refuses with the
degraded view named, never a silent absence that masks an unloadable
view.

**REQ-go-workspace** (behavior): A verification tree MAY be a Go
workspace: symbol resolution and witnessing span every `go.work` member,
and a member escaping the tree is refused — hermeticity is never silently
bent.

## Witnesses

**REQ-go-witness** (behavior): Witnesses MUST be derived from `go test -json`
output produced in the current verification run, correlating passed tests
with bindings of role `tests` or `proves` — both name executable test
symbols; toolchain cache replays are current-run
equivalent (the cache key is the tree content), and a bound test producing
no outcome in a witnessed run is unwitnessed and reads as `broken`; a
skipped test grants no witness without reading as `broken`.

**REQ-go-witness-class** (behavior): A witness MUST be classified `proof`
when its bound test's own body directly invokes the `stipulate/structural`
analyzer library (indirection through a helper does not classify),
`property` when it is a fuzz target (a function taking `*testing.F`), its
own body directly drives `pgregory.net/rapid` (a qualified or aliased
`rapid.Check` / `rapid.MakeCheck` selector call), or its own body directly
drives `github.com/leanovate/gopter` (a `Properties.TestingRun` selector
call — property registration and generator construction alone do not
classify, and a dot-imported call never does, for every recognized
library), and `example` otherwise;
the classification is resolved from the code, never declared. An
`example` classification carries a verdict naming what the bound body
lacks — a recognized library referenced without its classifying call is
named exactly (`rapid.Check not invoked in the bound body`; `no
structural assertion invoked in the bound body`), a recognized library
reached only through a dot import is named as such, a body touching
neither reads that no property driver or analyzer call appears, and a
non-runnable symbol is named as such — and an uncovered requirement's
report surfaces the classification verdict per bound witness beside the
required-evidence reason, a property-classified witness on a
proof-requiring cell named symmetrically.

**REQ-go-race** (behavior): A Go witness MUST derive only from a
race-enabled policy invocation, so every witness is race-attributed; an
accepted invocation running without the race detector contributes suite
health exactly like any other invocation and grants no witness evidence.

**REQ-go-policy-complete** (behavior): A Go policy execution MUST conserve
the complete suite semantics of its invocations: package build and exit
failures, `init` failures, `TestMain` failures, executable examples,
committed fuzz-seed replay (REQ-go-fuzz-exploration), packages without
named tests, and every workspace member (REQ-go-workspace) keep their
failure and selection behavior under the policy exactly as under a direct
`go test` of the same scope. A Go obligation crosses the wire as a
kind-prefixed identity — `package:`, `test:`, `example:`, `fuzz:`, or
`seed:` followed by the package path and, where applicable, the symbol or
seed file — and the conservation universe is the workspace's default build
selection as the executing host resolves it at load: obligations reachable only through an invocation's explicit tag
widening are a reviewed coverage addition, selected without an omission
finding elsewhere.

**REQ-go-owned-processes** (behavior): Every child process spawned for Go
policy execution or package discovery MUST run inside an owned, cancellable
process boundary whose entire descendant tree terminates with the
operation's cancellation — package loading owns its launcher's descendants
exactly as test invocations own theirs, and an ambient external package
driver never shapes verification.

**REQ-go-fuzz-exploration** (behavior): A fuzzing campaign MUST NOT feed the
gate directly — campaigns are time-bounded and nondeterministic; their
counterexamples enter the committed seed corpus, whose deterministic replay
in ordinary test runs is the witness.

**REQ-go-covers** (behavior): Tests MAY register requirement coverage at
runtime through the provided `Covers(t, id)` helper, which yields
subtest-granular registrations in the same run — attribution and
reporting, per REQ-evidence-witness; the witness itself follows the
bound test's outcome.

**REQ-go-covers-crosscheck** (behavior): A runtime registration naming a
requirement that has no matching binding of role `tests` or `proves` MUST
be a verification error; the binding store is the only claim source.

## Structural provers

**REQ-go-structural-provers** (behavior): The Go backend MUST provide
analyzer assertions — transitive import exclusions, exhaustive direct-import
allowlists with total package rows and optionally exact standard-library surfaces,
and interface satisfaction — authored as tests invoking the
`stipulate/structural` library, with the proof class resolved from the
invoking code exactly as witness classes are: never declared. Parameters
live in the test source, type-checked and reviewable, and the assertion
executes in the ordinary witness run.

## Slice

**REQ-go-slice** (behavior): Given symbol references, the Go backend MUST
return the declarations of their transitive dependency frontier —
signatures and named types declared within the module, rendered by the
object printer and shape-pinned, canonically ordered — returning facts
only: no depth budgets, no exemplar selection, no rendering policy.
Beside the declaration facts the backend reports the slice's
package-level sound floor: the symbols' packages' transitive in-module
import closure, each package carrying one disposition — `declared` when
the declaration frontier reaches it, `widened` when only the import
closure does (a reflection target has to be linked, init effects and
blank imports ride the import edge, and build-tag file selection is
resolved by the loaded view, so imports over-approximate every channel
the type graph misses and the floor never reads false-complete), and
`external` for a dependency edge leaving the loaded module members,
recorded at the boundary and not traversed (the standard library
excluded — stable under the pinned toolchain). The loaded view includes
test packages, so test-file import edges ride the floor — the test
channel is a dependency channel like any other.
Slice consumers judging packages judge from the floor, never from the
silently narrower typed frontier; the floor is a sound floor in the
closure-analysis sense: over-approximate, dispositioned,
never a silent gap. Enforced by `TestGoSliceFloorDispositions` and
`TestPartitionsJudgePackagesFromTheFloor`.

## Generated code

**REQ-go-generated-detect** (behavior): Generated Go files MUST be detected
by the standard `^// Code generated .* DO NOT EDIT\.$` marker, feeding
REQ-evidence-generated-code.
