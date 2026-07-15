# Binding surfaces

- [x] 1. Replace the hardening contract with binding-surface targets; retire obsolete mutation-specific issues and requirement bindings.
- [x] 2. Remove hardening report fields and the stranded kill-sheet witness type, introduce the initial binding-surface messages, and reserve retired dossier field identities.
- [x] 3. Settle semantic sets, validation, canonical ordering and identifier bytes, document discrimination, filtering, rendering, ownership, and the cross-tool sequencing before derivation code.
- [x] 4. Implement backend-independent binding-surface derivation and canonical identifiers, enforce their invariants with property and anchor tests, and resolve the validation and surface-identifier gaps.
- [x] 5. Publish authoritative valid and invalid cross-tool fixtures covering canonical IDs, mixed backends, shared requirements, empty bindings, empty reports, and malformed documents.
- [x] 6. Introduce `github.com/greatliontech/stipulator/bindingsurface` as a nested module with its own protobuf package and generated API, strict ProtoJSON codec, canonical validation and identifiers, and authoritative fixtures.
- [x] 7. Refactor root derivation and wire tests to the nested module, remove the root report-message declarations and duplicated canonical logic, bind the shared API, resolve the JSON and Go-wire gaps, and verify the module is release-ready.
- [x] 8. After gomutant accepts a released wire module and its fixtures under its own plan, replace the CLI `targets` export with exact filters, ProtoJSON stdout and atomic file output, and resolve the targets, filtering, and output gaps.
- [x] 9. Mirror the filtered report over read-only MCP inputs, delete the old target planner, exporter, staged classifier and their tests, resolve the MCP-tools gap, and disposition every issue whose MCP-surface trigger fires.
- [x] 10. Remove gomutant findings ingestion, hardening reminders and errors, findings/reminder readers, retired-store breadcrumbs and their tests from CLI and MCP gate paths.
- [x] 11. Delete the residual `internal/harden` package, Go body hashing and mutation-surface analysis, and their mutation-only tests and bindings.
- [ ] 12. Separately remove the syntax-based test-vacuity authoring heuristic without weakening ordinary binding resolution or witness execution.
- [ ] 13. Verify both repositories and their copied contract fixtures, retarget or close every resolving issue and reference, remove obsolete artifacts, and delete this plan.
