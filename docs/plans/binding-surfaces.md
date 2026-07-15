# Binding surfaces

- [x] 1. Replace the hardening contract with binding-surface targets; retire obsolete mutation-specific issues and requirement bindings.
- [x] 2. Replace hardening protobuf report fields and the stranded kill-sheet witness type with binding-surface messages, reserving removed wire names and numbers.
- [ ] 3. Replace `internal/harden` with backend-independent binding-surface derivation and canonical surface identifiers.
- [ ] 4. Keep `targets` as a filtered surface export and remove staged mutation classification, findings ingestion, gate reminders, and hardening CLI output.
- [ ] 5. Mirror target export over MCP while removing hardening reminder and staged-scope fields from its tools and results.
- [ ] 6. Remove Go body hashing, vacuity checking, mutation-surface analysis, retired hardening-store handling, and their tests and self-bindings without weakening ordinary binding resolution or witness execution.
- [ ] 7. Publish cross-tool contract fixtures and adapt gomutant to consume binding-surface targets, retaining ownership of mutation findings and measurement freshness; callers consume gomutant findings directly and apply their own acceptance policy.
- [ ] 8. Verify the repository and cross-tool boundary, retarget or close every resolving issue and reference, remove obsolete artifacts, and delete this plan.
