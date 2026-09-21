# The backend's own witnesses observe gofresh's memo store and never serve

Measured 2026-09-21 over this repository's own corpus (a cold full
check from an empty cache root, its records inspected): every witness
of internal/backends/golang (658 record variants), internal/cmd,
internal/check, internal/compile, internal/coverage, internal/verify,
and internal/witnesscache carries an `unverifiable` runtime-input
manifest whose entries read `runtime input not covered by observation
bracket: <cache root>/gofresh/<listing|filescan|observability|
scanfacts|testingscan|variantparse>/<key>.json` and `stat metadata
input: …` for the same files — the memo files the gofresh engines
those tests construct read and write under the process's cache home.
An unverifiable record never serves (REQ-evidence-witness-freshness's
fail-closed rule), so a plain warm `stipulator check` executes those
packages whole on every run: internal/backends/golang alone is the
race invocation's 2685s long pole, and the tool's own warm path —
51.3s at chunk 155's self-host verdict — is gone.

The long-lived store dates the change: this corpus's backend records
written through 2026-09-02 are all verifiable; from 2026-09-06 every
new one names the memo files. gofresh's memo root is resolved per call
from the environment (closure/internal/cachefile: SetRoot, else
os.UserCacheDir), so a test that redirects XDG_CACHE_HOME redirects
the memo — the observed files sit under the HARNESS's cache home, so
the tests in question construct engines under the inherited
environment. Which landing on 2026-09-06 turned the observation on
(the memo reads themselves, the bracket's coverage of the cache root,
or the tests' environment) is undetermined.

The memo store is a cache, never a record (gofresh's
REQ-closure-observability-memo: no memo position changes a verdict),
so a witness's outcome cannot depend on its content; declaring the
engine's memo root outside the observation — a policy-declared
exclusion or scratch namespace under REQ-inputs-scratch-namespace, or
a per-test memo root every engine-building test sets — restores
serving for the whole set. Both are soundness-sensitive placements
(the exclusion's argument rests on gofresh's memo clause) and want
the 2026-09-06 change identified first.

Lands: cross-tool train chunk 280 (chartered at 272's close: the
self-host warm path — identify the change, place the exclusion or the
per-test root, re-record, measure the plain warm check).
