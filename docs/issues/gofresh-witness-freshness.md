# gofresh-backed witness freshness: re-run only stale witnesses

Lands: when the gate or verify's full-suite re-run next proves too slow in
real use, or when witness records are next redesigned.

## Context

verify and gate witness a tree by running the whole test suite: a failing
unbound test breaks no binding, so suite health and witnessing are separate
questions, but the run itself is monolithic — every witness re-executes on
every gate, which on this corpus already takes minutes (the closure-heavy
suites dominate).

github.com/greatliontech/gofresh answers exactly this shape: a per-subject
fingerprint (source-closure hash + environment guards) with a verdict —
valid, stale, unverifiable — designed for caller-owned records
(REQ-fresh-fingerprint-data) and already consumed by pew and designed into
gomutant's record pins. A witness record carrying a gofresh fingerprint
re-runs only when its fingerprint is stale: the gate re-witnesses the delta,
not the world.

## Shape sketch (scoped, not designed)

- Witness outcomes persist with a gofresh fingerprint per witness symbol
  (subject = the test function; kind = CodeResult, so machine/runtime
  guards stay off).
- verify/gate check fingerprints first, run only the stale set, merge
  outcomes — absence of a fingerprint means run (the conservative default).
- The purity escape hatch (//gofresh:pure and the unverifiable verdict)
  applies to witnesses that read fixtures, exactly as in pew.
- Spec-first: evidence.md gains the freshness contract before any
  mechanism; gofresh stays a library dependency (it is a library by
  design — the document-seam rule here is specific to the mutation engine).

## Resolution

On landing: spec, implement behind verify/gate, promote this rationale,
delete this doc — git holds history.
