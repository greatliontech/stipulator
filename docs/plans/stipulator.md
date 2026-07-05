# Stipulator bootstrap

Spec: docs/specs/

- [x] 1. Manifest + corpus enumeration
- [x] 2. IR + record schemas (proto) + canonical hashing primitives
- [x] 3. Profile compiler (goldmark → IR) + lints
- [x] 4. Self-compile golden fixture (own spec as first corpus)
- [x] 5. Consistency verify: record stores, dangling/stale detection, pin backfill, CLI
- [ ] 6. IR diff + layout-independence check
- [ ] 7. Go backend static resolution + shape hashing
- [ ] 8. Go witnesses (`go test -json` correlation + Covers helper)
- [ ] 9. Coverage policy + report buckets + gate
- [ ] 10. Dispositions
- [ ] 11. Gap landing conditions
- [ ] 12. Proto backend (protocompile, descriptor hashing, assertions)
- [ ] 13. Bundles (closure computation + export)
- [ ] 14. Generated folder indexes (`fmt` + freshness lint)
