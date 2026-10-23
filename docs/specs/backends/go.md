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
The package boundary is the longest declared import-path prefix followed by
`.`; dotted path elements are not receiver separators. Package metadata is
read under the selected views before capture and scoped typed loading. Each
queried candidate needs a declaration or an explicit absence answer; a missing,
malformed or failed answer never establishes absence. Receiver and function
interpretations are bounded lookup candidates, not authority to widen a named
operation to the whole tree.

**REQ-go-shape-hash** (wire): The Go shape hash MUST be computed over the
object's declaration as rendered by the Go type-checker's object printer
with full package-path qualifiers, per REQ-model-hash-canonical-form; the
rendering is toolchain-versioned, so a toolchain change may re-stale shape
pins, restored by re-pinning.

**REQ-go-static-binding** (behavior): Static binding verification MUST
resolve the symbol through the Go type checker and compare shape hashes; a
package load failure is a verification error, not an absence.

**REQ-go-load-attribution** (behavior): A package load failure MUST
surface classified: a failure rooted in an imported dependency names the
unresolved import, its providing module, and every committed directive
that can apply to the import, composed together — disagreeing member
requires all named in semantic-version order with selection left to
minimal version selection, version-scoped replaces rendered beside
whatever they condition and their absence of company stated when
nothing stands beside them, and only an all-versions replace standing
above the requires it supersedes — derived from the committed tree
alone, so one commit yields one attribution on every run, and rendered
single-line beside the loader's own diagnostic with its position where
the loader supplies one. Apart from a separately established disposable-export
failure under REQ-go-export-cache-failure, an in-tree failure, an unrecognized shape, and
a package whose own module cannot be identified surface the loader's
diagnostic unchanged, never a guessed attribution; the witness
classifier — the backend surface every class rendering consumes —
answers an unloadable bound symbol with the load failure as its verdict
rather than a class derived from the absent body, so no consumer can
present the absence as a classification, while the user-facing naming
of the state rides the resolution error's verification problem.

**REQ-go-export-cache-failure** (behavior): A typed-load failure with an
established missing disposable export dependency MUST name the affected
package and missing export and give an actionable rebuild or retry remedy
under the same selected directory, environment and build flags. The class
requires structured dependency/export metadata and an observed absence within
the effective selected Go build cache; diagnostic text alone, an ordinary
source error, or an unexamined path cannot establish it. The loader's original
diagnostic remains available, and a missing export does not establish why the
cache entry disappeared. A bounded refresh under the same selection is
permitted; an unsuccessful or unattempted refresh leaves the operation
explicitly refused. Neither path may substitute a
previous typed answer, widen the selection, or turn an unresolved load into
an absent declaration or successful witness classification. Unrelated package
answers retain their own load disposition.

**REQ-go-build-selections** (behavior): Symbol resolution MUST span the
accepted policy's build-selection dimensions - one package view per
distinct invocation (module root, workspace mode, effective tag-set, toolchain,
module mode) configuration beside the default views (no tags, the ambient
toolchain, the toolchain's own module mode). The tree's default view comes
first; additional accepted module-root/workspace contexts get their default
views in canonical invocation order before any non-default selection.
Workspace-on views include the declared workspace members; workspace-off
views include their selected module alone. The effective tag-set is the
declared tags plus the implicit `race` tag of a race-detecting
invocation and the module mode is the record's declared module mode,
an undeclared mode being the toolchain's default for the tree
(a vendor tree and the module cache resolve one import to different
package graphs, so a view loaded under the wrong mode misresolves the
witnesses' own symbols; the PGO profile shapes the binary, never
resolution, and stays out), each view loading under its own selection's
toolchain and module mode exactly as its invocation executes, derived
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
refuses with the unresolvable selection named, never a silent absence. The default views are consulted first and
then each distinct declared selection in the policy's canonical invocation order; the
first declaring view supplies the resolution and shape hash, and the
declaring view's file is the symbol's file for impact and freshness
keying, so a tag-gated edit invalidates the witnesses it declares. A
tree without a policy record resolves the default view alone; a
malformed record is a verification error, never a silent narrowing to
the default view. A declared selection that cannot load degrades to a named
refusal - binding stays healthy for every symbol the loaded views
resolve, while a reference they cannot answer refuses with the
degraded view named, never a silent absence that masks an unloadable
view.
Declaration filenames and source locations are physical positions in the
declaring source, not diagnostic positions remapped by `//line` directives.
Generated-code judgment and test-file membership use that same physical
source. A promoted declaration held only as export data is located in its
declaring package under the resolving view's selection; unavailable or
inconsistent declaring source refuses resolution rather than borrowing a
diagnostic filename. Loader diagnostics retain the positions the loader gives.

**REQ-go-workspace** (behavior): A verification tree MAY be a Go
workspace: symbol resolution and witnessing span every `go.work` member,
and a member escaping the tree is refused — hermeticity is never silently
bent.
An independent module root explicitly selected by the accepted policy is also
a resolution member under its own workspace mode; it need not be added to a
workspace or required by the root module. Module roots remain inside the
verification tree after symlink resolution. Undeclared nested modules are not
implicitly added to the conservation universe.

## Witnesses

**REQ-go-linker-selection** (behavior): An invocation declaring
`linker_strings` MUST preserve these selection and authority obligations:

- **admission** — Go string-variable linker assignments are sorted by
  `(package, variable)` with no duplicate target. Each entry names an import path
  and Go identifier and exactly one source: literal `value` (including empty), or
  `value_environment`, a nonempty portable environment-variable name. Environment
  sources resolve once from the admitted invocation environment after its denials
  and overrides; unset or empty environment values refuse. Values contain only
  printable ASCII without whitespace, quotes or backslashes; unsupported values
  refuse rather than being expanded or truncated. The closed selection supplies
  only `-X package.variable=value`, never arbitrary linker flags, pattern overrides,
  shell commands or source substitutions. The linker's own applicability rules
  still apply: an assignment does not prove a target is linked, is an eligible
  string variable, or is consumed by a test.
- **propagation** — The same resolved assignments reach every retained test
  compilation and native build description, including whole-policy execution,
  selective witnesses and rebuilds or isolation retries. Sharing an artifact
  preserves its selection; another attempt cannot fall back to an unlinked
  default. These assignments partition capture groups and build guards; declared
  targets and value sources partition record identity. An environment-resolved
  value is a producing build input, not a new declared record coordinate.
  Invocation reports retain the resolved assignments. Existing invocations without
  assignments retain their record coordinates.
- **authority** — Ambient and explicit GOFLAGS still refuse `-ldflags` and
  unsupported substitution controls; binary `args` is not another build-flag
  channel. CLI and MCP use the same policy normalization and execution. Selection
  governs the runner's own compilation, not child builds a test independently owns.
  Producing a value and establishing its claimed relation to source remain the
  consumer producer's responsibility, not a policy hook or build certification.

**REQ-go-invocation-input-files** (behavior): An invocation declaring
`input_files_from_environment` MUST preserve these selection and authority
obligations:

- **admission** — The declaration is a sorted unique list of portable environment
  names. After environment denials and overrides, each name resolves once to a
  clean absolute file path. Absent or empty values and duplicate paths refuse
  admission. Before execution, each selected input is an existing regular file
  without symlink components and raw-byte eligible under the native observation
  owner's actual producing directory, complete environment and classification
  declarations. Excluded, recordless, projected or unverifiable inputs refuse;
  no consumer prefix approximation or bracket override can confer eligibility.
- **attempt-binding** — Before execution or warm checking, the prepared actual or
  prospective retained runtime environment is validated for this eligibility and
  each selecting environment value's agreement with its frozen association. Every
  execution attempt repeats this validation. Failure prevents execution or serving
  under that invalid selection, even when native freshness preparation is otherwise
  unavailable; it creates neither observations nor a new historical-record judgment.
- **evidence** — Exactly the selected files augment the ordinary observation
  bracket; their parents and siblings gain no admission. The resolved paths appear
  in invocation reports, and the declarations participate in record identity.
  Declaration does not claim that each test reads the file. An observed dependency
  still requires the actual read and its independently governed observation,
  outcome, closing and current-check obligations. A matching content guard neither
  validates assertions inside a manifest nor establishes that it describes the
  source actually built. These declarations execute no producer, create no missing
  file or observation, and grant no purity or semantic approval. The consumer
  retains any required producer lifecycle, source-to-manifest relationship and
  hold-still obligations.

**REQ-go-witness** (behavior): Witnesses MUST be derived from Go test JSON
events produced in the current verification run, correlating passed tests
with bindings of role `tests` or `proves` — both name executable test
symbols; toolchain cache replays are current-run
equivalent (the cache key is the tree content), and a bound test producing
no outcome in a witnessed run is unwitnessed and reads as `broken`; a
skipped test grants no witness without reading as `broken`.

**REQ-go-retained-test-execution** (behavior): Package execution MUST build a
retained test artifact under the admitted complete compilation description,
then execute that artifact under its package-runtime description. Compilation
and execution cannot exchange environments or working directories. The build
retains Go's package compilation and vet semantics, including build failures
and packages with no runnable test artifact; a package without an executed
test process gains no fabricated execution or observation evidence.
The complete actual runnable roster is fixed before the process begins;
original executions and isolation retries own separate native transactions,
even when they reuse an unchanged artifact and immutable preparation facts.
Stipulator's owned, bounded streaming conversion of the Go test output protocol
supplies the JSON event stream without owning or replacing the test binary's
termination. Framing and escape interpretation are independent of input read or
write segmentation. EOF emits an unterminated fragment as diagnostic output,
then flushes already-parsed per-test reports exactly once; it never completes a
fragment into framing or invents a package PASS or FAIL. Conversion introduces
no synthetic test-output framing and retains no whole-stream or unbounded
physical-line buffer or spool. The binary's
exact exit status, signal, original harness completion and conversion success
remain separate facts. A synthesized package-health event cannot establish
process completion, and conversion failure cannot yield a complete witnessed
stream. Build, execution and conversion remain inside the owning cancellation
and process-accounting boundary, with their actual descendants accounted for.
Retained execution preserves the accepted invocation's runtime flags from
GOFLAGS, typed configuration and raw arguments, including their ordering,
precedence and positional parsing. It also preserves Go's runtime PATH,
coverage and profile output behavior, fuzz-seed replay, examples, TestMain and
timeout semantics under the policy. Runtime output handling cannot substitute
converter success for a failed or incomplete test process, nor turn a passing
test prefix into package health. Native finalization and checking provide the
reusable evidence and judgments; consumer publication selects an independently
eligible attempt without rebuilding the native evidence-selection ladder.

**REQ-go-witness-class** (behavior): A witness MUST be classified `proof`
when its bound test's own body directly invokes the `stipulate/structural`
analyzer library (indirection through a helper does not classify),
`property` when it is a fuzz target (a function taking `*testing.F`), its
own body directly drives `pgregory.net/rapid` (a qualified or aliased
`rapid.Check` / `rapid.MakeCheck` selector call), or its own body directly
drives `github.com/leanovate/gopter` (a `Properties.TestingRun` selector
call — property registration and generator construction alone do not
classify, and a dot-imported call never does, for every recognized
library), or its own body directly drives the standard library's
`testing/quick` (a `quick.Check` / `quick.CheckEqual` selector call —
`quick.Value` alone generates and does not classify), and `example`
otherwise;
the classification is resolved from the code, never declared. A
`property` classification additionally states its seeding: a
driver-quantified body is random-seeded — the driver draws the inputs
it quantifies over from a run-time seed (`testing/quick` from its
`Config.Rand`, the wall clock's when nil; a configuration carrying its
own source classifies the same, the classification being static) —
while a fuzz target's
ordinary run replays its committed seeds deterministically and is not;
the seeded form is what freshness serving consults
(REQ-evidence-witness-freshness-seeded). An
`example` classification carries a verdict naming what the bound body
lacks — a recognized library referenced without its classifying call is
named exactly (`rapid.Check not invoked in the bound body`; `no
structural assertion invoked in the bound body`), a recognized library
reached only through a dot import is named as such, a body touching
neither reads that no property driver or analyzer call appears, a
non-runnable symbol is named as such, and a driver reached only
through in-module helpers names the first hop with the remedy (the
tier is direct-call by contract: drive the runner in the bound body, a
completion guard riding as the test context it is handed) — and an
uncovered requirement's
report surfaces the classification verdict per bound witness beside the
required-evidence reason, a property-classified witness on a
proof-requiring cell named symmetrically.
Only a runnable test or fuzz declaration in a physical `_test.go` file can
classify above example. Its entry-point syntax follows Go's discovery rule,
and its resolved pointer parameter denotes the corresponding `testing.T` or
`testing.F`, including aliases. A helper taking a testing handle, a method,
or a production function does not acquire witness authority from that type
or from a remapped diagnostic filename.
A runnable fuzz declaration retains its harness-derived property class when
implemented by a linked function rather than a Go body.

**REQ-go-race** (behavior): A Go witness MUST derive only from a
race-enabled policy invocation or a non-race invocation explicitly admitted
through `plain_witness`, with tier attribution following
REQ-check-witness-selection. An accepted non-race invocation without that
admission contributes suite health exactly like any other invocation and
grants no witness evidence.

**REQ-go-policy-complete** (behavior): A Go policy execution MUST conserve
the complete suite semantics of its invocations: package build and exit
failures, `init` failures, `TestMain` failures, executable examples,
committed fuzz-seed replay (REQ-go-fuzz-exploration), packages without
named tests, and every workspace member (REQ-go-workspace) keep their
failure and selection behavior under the policy exactly as under a direct
`go test` of the same scope.

**REQ-go-policy-complete-obligation-identity** (behavior): A Go obligation MUST cross the wire as a
kind-prefixed identity — `package:`, `test:`, `example:`, `fuzz:`, or
`seed:` followed by the package path and, where applicable, the symbol or
seed file — and the conservation universe is the workspace's default build
selection as the executing host resolves it at load: obligations reachable only through an invocation's explicit tag
widening are a reviewed coverage addition, selected without an omission
finding elsewhere.
Named test and fuzz discovery follows Go's entry-point syntax: a top-level
name under Go's prefix rule, one pointer parameter whose type name is `T` or
`F` (bare or qualified), no result and no type parameters. A local or re-exported alias may
supply the handle; discovery does not require a particular import in that file.
Compilation determines type compatibility. Build failure remains a package
obligation even when a test-shaped declaration cannot type-check. `TestMain`
with the `T` shape is an ordinary test; the `M` harness wrapper belongs to the
package obligation. Discovery alone grants no witness or classifier tier.

**REQ-go-owned-processes** (behavior): Every child process spawned for
Go policy execution or package discovery MUST run inside an owned,
cancellable process boundary whose entire descendant tree terminates
with the operation's cancellation — package loading owns its launcher's
descendants exactly as test invocations own theirs, and an external
package driver never shapes verification — an ambient one is refused,
and the curated environment's package-driver variable is pinned off as
its last word, past every declared override or denial.

**REQ-go-owned-processes-telemetry-home** (behavior): The toolchain's
telemetry, on by default, forks a detached upload sidecar outside that
boundary on its daily check, and no variable names its directory: on
unix the environment every Go child runs under — the witnesses', the
loads', the toolchain queries' — therefore MUST point the toolchain's config
home (`XDG_CONFIG_HOME`) at an owned home — a directory of the user's
own, private, never a symlink, under a parent nobody else can rename it
out of, under the user cache, or under `/tmp` when the cache cannot host
it so a verb that loads symbols still runs where only the witness store
refuses — one per source config home (the child's own `XDG_CONFIG_HOME`,
else its own `HOME`'s `.config`; a child with neither has no config home
and nothing to own; one directory is one source whatever its spelling,
and a source that is not absolute is a refusal), holding
`go/telemetry/mode` = `off` — created once, never swept, nothing written
under it by a toolchain whose telemetry is off, and re-established,
through the same secured root, before every spawn that reuses a derived
environment — each witness process's, each package listing's. The owned
home's path is a recorded environment coordinate: its stability is the
host's. The whole config home moves with it; what a Go child reads there
is pinned back to the source home: `GOENV` names the source home's go
env file unless the environment names one, and the owned home's
`git/config` names the source home's ignore and attributes files as
git's defaults and includes the source home's config, so a child's go
configuration and its git's global configuration do not move with the
telemetry. An owned home that cannot be prepared is a refusal naming the
reason. On platforms whose config home no variable selects, the
toolchain's detached telemetry is the one sanctioned escape from the
boundary.

**REQ-go-owned-processes-runner** (behavior): Every go child but the loader's and the provenance probe's
MUST run through Gofresh's go-command runner carrying this boundary as its
containment — the group, the envelope expiry's quit and grace, the
policy's wait delay — the analysis engines' own commands through the
same runner installed on every engine, and the observation facade's
classification-root probe (a bare `go env -json` in the package
directory, which forks the C compiler the toolchain configures — a
descendant the boundary sweeps — memoized per package directory and
environment for the judged operation's life) through it with a probe's
bounded reap; the provenance probe (`go env GOVERSION`, naming its
key, so forking nothing) runs through a plain runner in its caller's
group with the bounded reap; a retained witness process's standard output
and standard error form one stream, read to its end by the invocation itself.
A descendant retaining either output holds that stream open until the
envelope's expiry sweeps the group; reader completion cannot substitute for
binary termination, and output drainage cannot end an unfinished binary's
quit/grace escalation. A listing — collected by the
boundary, with no wholeness test of its own (a truncation at an object
boundary reads as a shorter set) — is refused naming the hold whenever
the wait delay expires before the listing is drained (Gofresh's listing
form), and a listing exiting nonzero answers nothing. The Go
children are `go env` (the workspace's and normalization's samples — the
normalization's the toolchain's one environment snapshot — through the
owned boundary; the provenance probe's toolchain sample, a
descendant-free query in its caller's own process group), `go list`
(discovery's listings through the owned boundary; the package loader's
— spawned by an in-process loader
rather than through the owned command boundary, run by the resolver
child's typed views, by the analysis engines the parent process
hosts, and by a witness's own structural proofs — each swept with the
operation that owns it),
`go test -c` (retained compilation through the owned boundary),
and the analysis engines' own `go version` and `go env` samples (descendant-free
queries, under the boundary through the engines' runner); every one
runs under the owned environment. Retained test binaries use the runner's
consumer-program form under the same ownership policy and their admitted
package-runtime environments. Output conversion runs inside Stipulator under
the operation's cancellation boundary; it is not a child process. A reader,
writer or conversion fault cannot establish a complete witnessed stream.

**REQ-go-owned-processes-resolver-child** (behavior): This binary's
own child, the self-executed resolver child, is no Go child: it MUST be
spawned by the consumer-command form of Gofresh's go-command runner,
under a containment of its own — its own process group swept outright
with its operation (no quit arm: the child's operation is never an
envelope), the reap bounded by the policy's wait delay — and under the
parent's own environment normalized under the policy (a malformed
entry refuses the spawn); on Windows the tree-kill helper is the
boundary's mechanism.

**REQ-go-owned-processes-child-identity** (behavior): The
resolver child's handshake line — ready or the tree's load error alike —
carries the child's executable identity (the size and modification time
of the file it runs as and, where the platform exposes one, its inode,
sampled when its process starts), and the parent MUST refuse, before reading
anything else the child says, a child whose identity is not the file the
parent chose to spawn: its own image as sampled at its own start on the
self-executed path, so a binary replaced under a running server is a
refusal naming both sides rather than a build answering with fields this
one never wrote; a child answering no identity is refused as an older
build; a parent that cannot read its own image refuses every child.
Enforced by `TestSelfExecutedChildIsRefusedWhenTheParentsImageMoved`,
`TestSelfExecutedChildsLoadErrorIsItsOwn`,
`TestResolverClientRefusesAChildOfAnotherBuild`, and
`TestFileIdentityMovesWithTheFile`.

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
