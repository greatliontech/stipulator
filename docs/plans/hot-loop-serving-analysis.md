# Analysis: stipulator's life in the hot loop

Pre-code deliverable for the hot-loop-serving plan. Measured against the
largest real corpus (gofresh: 70 requirements, 169 bindings, 465 top-level
tests) and stipulator's own (~123 requirements); all byte counts are live
measurements over stdio unless marked derived. Deleted with the plan at
close-out; git holds history.

## 1. The lifecycle contract: worktree content is the only truth

Stipulator's entire persistent truth is keyed on **worktree content** — never
the git index, HEAD, branch, or history. Requirement pins hash normalized
clause text (position/file independent, `internal/canon/canon.go:19-30`);
shape pins hash the declared type only (`internal/backends/golang/golang.go:295-301`);
witnesses are gofresh fingerprints over the test's whole non-stdlib closure
read from disk (`internal/witnesscache/witnesscache.go:107-119`). Records
(`.stipulator/`) are committed and travel with checkouts; the witness cache is
repo-local, gitignored, per-worktree.

Consequences, walked from code:

| Git state | Behavior | Direction |
|---|---|---|
| Clean at HEAD | records re-prove against current tree | correct |
| Dirty unstaged | witnessing pins the **dirty** bytes; the committed tree is never witnessed unless byte-identical | correct by design — verification is a function of tree state |
| Staged A, worktree B | the index is invisible; everything sees B — consistent with an agent reasoning over `git diff HEAD`. Committing A commits content never witnessed; the later fingerprint mismatch re-runs (safe, cost-only) | correct |
| Branch switch / rebase / older checkout | a branch-A witness serves on branch B iff closure + guards + runtime digests match — verification by proven equivalence (REQ-evidence-witness-freshness) | correct; but **one record per `package.Test`** (`witnesscache.go:264`) means branch ping-pong evicts and re-runs every crossing |
| Stash pop | next fingerprint check re-proves; in-run drift moves the observation bracket → unverifiable → re-execute; the exact content+metadata revert inside the run span is specced-unprovable (`evidence.md:130-134`, filed) | correct |
| Second worktree | fully split store; cold worktree re-runs once even on identical content | correct, cost-only |
| Concurrent processes | witness cache: temp+rename, last-writer-wins, whole-document drop on any invalid record — never wrong, only lost memoization; **record verbs: load-compute-write last-writer-wins — concurrent writers silently drop each other** (filed: concurrent-record-writes) | records are the one wrong-direction concurrency residue |

The two structural cost bugs on this contract (not correctness bugs):
**(a)** whole-cache drop on a single invalid record (`witnesscache.go:257-271`)
— one moved test discards every other test's memoization; **(b)**
single-variant records — branch alternation thrashes. Both live where plan
chunk 3 (cache to UserCacheDir) already operates.

`gitfs.Changed` (worktree-vs-HEAD, staged+unstaged) exists and is
production-dead — the exact primitive an impact preview ("which requirements
does this diff touch") needs, joined with the bindings' symbol→requirement
map, without executing anything.

## 2. Response envelopes: no surface has a budget

Measured at gofresh scale (MCP wire ≈ 2× payload — every result ships as text
content **and** structuredContent, `internal/mcpserver/server.go:1033`):

| Surface | Bytes today | Growth | Bound |
|---|---|---|---|
| check (CLI --json / MCP) | ~200–300KB healthy, derived | O(tests + bindings + requirements + failures×64KiB) | **NONE — no view, no scope; takes struct{}** (`internal/check/check.go:106-135`) |
| context, 70 ids | 521KB (625KB with slice) | O(ids × 7.4KB avg dossier, 23KB max) | none per-id or total |
| partitions, default on red tree | 188KB | components O(ids) + **overlaps O(C²)** (`internal/facts/facts.go:190`); 761 pairs today → ~186k at 10× | none; empty ids = all reds |
| verify view=bindings (MCP) | 141KB | O(bindings) | scopes, no row cap |
| read_spec, all ids | 157KB | O(closure) | uncapped list |
| compile --ir / diff | 196KB / O(delta) | full passthrough | none |
| targets | 17KB / 35KB MCP | O(bindings) | filters, no cap |
| verify/gate summaries | 0.5–2KB | counts + ids (+ unbounded PackageFailures map in verify summary, `internal/views/views.go:278`) | mostly good |

Worst case is bounded per-diagnostic (64KiB + Truncated flag,
`internal/backends/golang/execute.go:37`) but unbounded in **count**: an
all-red gofresh check ceilings at ~29MB (~60MB MCP); a 10× corpus at ~300MB
per call. The healthy-tree check — the loop's most frequent call — has no
reduction mechanism at all.

Existing vocabulary (summary/reds/full views, ids/bucket/filter/path scopes,
`--quiet`, per-diagnostic caps) is opt-in and absent exactly where it matters
most (check). Nothing has pagination or a total byte budget.

## 3. The loop as an agent lives it

| Step | Today | Cost |
|---|---|---|
| Write REQ → green tree | compile, then one `gap` call **per new REQ** (MCP gap is single-requirement; no self-landing sentinel) | seconds, N round trips |
| Implement + bind | one `bind` per claim, each spawning backend + `go/packages` load | seconds per call, dozens on migration |
| Edit code | nothing answers "which REQs did this diff touch" without paying execution | — |
| "Am I still green?" | `check` — full policy execution, zero serving ("the fresh count is structurally zero", `check/check.go:106-111`), minutes; a real agent's check died at its 600s client cap; response is simultaneously the **largest** payload, truncated client-side before the verdict | the loop killer |
| Commit | check → prune (runs the **full verify pipeline with tests**, `server.go:771`) → check again | worst case 3 full suite executions |

The verdict call being at once the slowest and the largest, with no view and
no scope, is the single sharpest fact of this analysis.

## 4. Imperative that should be automatic (ranked by loop pain)

1. Full-suite re-execution for every verdict — serving is the fix (chunk 2).
2. Shape re-pin after benign refactor: verify already prints "shape moved
   while every behavior witness stayed green" (`verify.go:267-270`) yet the
   failure never names `pin` — the tool computes the remediation and
   withholds it.
3. Editorial spec edit → every binding stale; no batch "these N changed;
   editorial?" consent flow.
4. Rename/move → dangling symbol, manual unbind/bind per binding, no
   shape-hash-matched candidates.
5. Witness-staleness attribution: engine knows which tests an edit staled;
   surface prints counts (chunk 4).
6. Dangling-record repair requires hand-editing `.stipulator/` — the gap verb
   validates against the corpus so it cannot retract what the corpus no
   longer contains (chunk 5).
7. Prune residue names its command but is never an offered action (chunk 7).
8. Registration↔binding dual bookkeeping heals by hand.

## 5. Guidance surfaces

Failure-message audit: witness/policy/CLI-root/typo'd-enum failures name the
next action; **shape-moved, stale-content-pin, dangling-record, MCP-root, and
empty-targets do not** (cites in the audit: `coverage.go:229,234,238`,
`verify.go:330,461,484`, `server.go:228-244`). First contact: `init` teaches
compile, then the chain stops — nothing teaches bind→gap→check order; there
is no README. **The MCP server ships zero server instructions**
(`mcp.NewServer(..., nil)`, `server.go:99`): an agent cannot learn loop
order, per-tool cost expectations (check's description omits that it executes
the full policy), root discovery, or that targets keys on implements-bindings.

## 6. Spec tensions found

- Witness-cache digests are 16-hex (sanctioned by
  REQ-evidence-witness-cache-format) while REQ-model-hash-func requires
  64-hex for hashes "observable in outputs or stored records" — reconcilable
  only by reading the cache as neither; wants a cross-reference sentence.
- `gitfs.Changed` is spec-silent latent surface.

## 7. Design implications mapped onto the plan

Confirmed as-planned: chunk 2 (serving check — the keystone; every number
above says the verdict must serve), chunk 3 (UserCacheDir — and it should
also fix the whole-cache drop and single-variant eviction found in §1),
chunk 4 (per-test staleness attribution), chunk 5 (dangling-record
lifecycle), chunk 6 (server instructions + progress-token audit), chunk 7
(offered actions).

Gaps the plan does not yet cover, surfaced for decision:

- **A response contract**: every MCP tool result fits a declared byte budget
  by construction — summary-first defaults everywhere (check gains
  views/scopes; it has none), pagination or artifact-handoff for
  bindings/context/partitions-class payloads, the partitions overlap term
  capped or scoped, and the 2× text+structured duplication dropped to one
  encoding.
- **Impact preview**: `gitfs.Changed` + bindings as a cheap read-only
  "this diff touches REQ-x, REQ-y; witnesses A, B stale" — the missing
  edit-time primitive (§3 step 3, §4 item 5).
- **Commit-path cost**: prune stops re-running the world (fold into chunk 5
  or 7).
- **Remediation-naming floor**: every broken/dangling state names its repair
  verb (extends chunk 7 beyond offered actions).
- Spec cross-reference for the hash-width tension (§6).
