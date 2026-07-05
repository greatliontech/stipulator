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
- [ ] 9. Coverage policy + report buckets + gate
- [ ] 10. MCP server (modelcontextprotocol/go-sdk): compile/verify/coverage tools; gitfs adapter (go-git) for diff-against-revision
- [ ] 11. Dispositions
- [ ] 12. Gap landing conditions
- [ ] 13. Proto backend (protocompile, descriptor hashing, assertions)
- [ ] 14. Bundles (closure computation + export; MCP bundle tool)
- [ ] 15. Generated folder indexes (`fmt` + freshness lint)
