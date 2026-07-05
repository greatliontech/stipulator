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
- [ ] 13. MCP server (modelcontextprotocol/go-sdk): compile/verify/coverage/bind tools; verify+coverage report messages (wire); gitfs adapter (go-git)
- [ ] 14. Context facts: closure seeds (spec-neighborhood bindings), policy-free symbol slice, candidate partitions by slice disjointness
- [ ] 15. Dispositions
- [ ] 16. Proto backend (protocompile, descriptor hashing, assertions)
- [ ] 17. Bundles (closure computation + export; MCP bundle tool)
- [ ] 18. Generated folder indexes (`fmt` + freshness lint)
- [ ] 19. Go structural provers (import constraints, interface satisfaction)
- [ ] 20. Property-test hardening: invariant coverage to property witnesses
- [ ] 21. Attestation records + change-signature classifier + determinism harness + manifest policy overrides
