# Legacy-path rewire: one policy, witness-only serving, brackets

Spec: docs/specs/overview.md, docs/specs/evidence.md, docs/specs/change.md, docs/specs/mcp.md

- [x] 1.1 Triage gate: disposition reds-only (fold, ch4), runtime-input (fold,
      ch5), coverage-forces-out (redefer H2-lifecycle), manifest-union (park).
- [x] 2 Spec amendment (isolation sentence) + executor selection:
      testCommandArgs/runPackage take a top-level selection; -test.run joins the
      reviewed-args collision refusal; retry loop isolates abort-shadowed AND
      green-in-red-process tests under the invocation envelope; fixtures.
- [x] 3 Selective witness runner: policy load (fail on ErrRecord), discovery-
      derived expected set, capture groups reused, serve/execute/publish keyed
      on selective-process disposition, post-run served-record revalidation
      with one in-run retry, empty-served degrade, outside-policy count in
      report + views; disjointness pinning test.
- [x] 4 Rewire consumers + kill: cmd verify/gate/prune + mcpserver runTests
      switch; delete legacy runners per kill list; repoint drift/ungated-pass
      fixtures; fire+prune core-one-execution gap; close reds-only issue.
- [x] 5 gofresh bump + brackets: pkgDirs plumbing, pre-spawn module-relative
      bracket, WithBracket in completedObservation + fuzzer per-iteration
      bracket + grammar extension; delete packageDir; close runtime-input
      issue (promote root policy into evidence.md/comment).
- [ ] 6 Close-out gate: wrap-aware cite sweep, retarget Lands, delete plan.
