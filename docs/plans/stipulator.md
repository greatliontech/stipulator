# Stipulator bootstrap

Spec: docs/specs/

- [x] 1. Manifest + corpus enumeration
- [x] 2. IR + record schemas (proto) + canonical hashing primitives
- [x] 3. Profile compiler (goldmark → IR) + lints
- [x] 4. Self-compile golden fixture (own spec as first corpus)
- [x] 5. Consistency verify: record stores, dangling/stale detection, pin backfill, CLI
- [x] 6. IR diff + layout-independence check
- [x] 7. Go backend static resolution + shape hashing
- [x] 8. Go witnesses (`go test -json` correlation + Covers helper)
- [x] 9. Coverage policy + report buckets + gate
- [x] 10. Evidence ladder v2: property/example witness split, run attributes (-race), fuzz-as-exploration
- [x] 11. Record verbs: bind/unbind/gap (tool-owned record authoring)
- [x] 12. CI gate: Taskfile + workflow
- [x] 13. MCP server (modelcontextprotocol/go-sdk): tools, resources, report wire messages; bundles pulled forward
- [x] 14. Context facts: closure seeds (spec-neighborhood bindings), policy-free symbol slice, candidate partitions by slice disjointness
- [x] 15. Dispositions
- [x] 16. Witness hardening: vacuity rejection at bind, body hashes, harden verb (operators + overlay runner), hardening records, nightly sweep
- [x] 17. Bundles (closure computation + export; MCP bundle tool) — landed with chunk 13
- [x] 18. Generated folder indexes (`fmt` + freshness lint)
- [x] 19. Go structural provers (import constraints, interface satisfaction)
- [x] 20. Property-test hardening: invariant coverage to property witnesses
- [x] 21. Per-symbol kill-sheets: harden records re-keyed to (symbol, body hash, witness set)
- [x] 22. Mutation engine v2: type-aware statement noop-ification, attested-equivalence survivor dispositions, extended operator families
- [x] 23. Manifest policy overrides ((kind, keyword) → minimum evidence)
- [x] 24. Attestation evidence records (weakest rung, distinct in every output)
- [ ] 25. Determinism harness (kill attribution, operation coverage, environment pins)
- [ ] 26. Change-signature classifier (rearchitecture vs semantic drift)
- [ ] 27. gitfs adapter (go-git) for diff-against-revision
