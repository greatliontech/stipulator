# Records verification encounters vanished Go export-cache files

Lands: user decision

Field report from bldc on 2026-10-08, at base commit `0863f9d`, before its
parent-relation refactor. This records a transient resolution failure; it does
not establish why the cache files vanished or claim a false passing verdict.

Command:

```sh
mlock run stipulator verify --no-test --view bindings --req REQ-source-edge-meaning
```

The invocation reported load errors for standard-library imports while resolving
bindings across the corpus, for example:

> resolving github.com/greatliontech/bldc/internal/compile/imaging.Pass:
> package .../imaging has load errors: imaging.go:23:2: could not import fmt
> (open /home/nikolas/.cache/go-build/70/705e6da556a20ae04ce513a88ebe8c88b4e2d1b40388bdd20bea278450d5c6b9-d:
> no such file or directory)

Other missing export paths affected `flag`, `errors`, `slices`, `bytes`, and
`maps`. The summary still listed four scoped binding rows as current/resolved,
with 1,574 resolutions served from records and 16 resolved typed. The run took
1m13.8s; no clean-verification claim was made from it. The harness did not retain
an independently reliable process exit status, so this report makes no claim
about the command's exit code.

Subsequent ordinary Go tests, binding authoring, and a full canonical check
could load the packages. The later check's only failure was an independently
explained moved test shape; after reviewed shape re-consent, the canonical check
passed. There was no cache deletion by this session. External removal, metadata
reuse, and recovery through intervening Go commands have not been isolated.

The installed binary inspected afterward was v0.73.6 at `c01a298`, built with
Go 1.27.1 and depending on gofresh v0.112.1. That observation does not prove which
binary a concurrent upgrade may have left running during the first command.

Investigate export-path lifetime across resolution capture and typed loading.
Possible responses are bounded metadata refresh for a vanished disposable
export, or an actionable retry/rebuild diagnostic when refresh cannot safely
establish the view. Preserve explicit resolution failure when recovery fails;
never serve a stale typed view merely to suppress the diagnostics. The tool
owner sequences this investigation.
