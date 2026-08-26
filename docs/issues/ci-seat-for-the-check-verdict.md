# Does `stipulator check` get a CI seat?

CI (added with cross-tool train chunk 107) runs build, vet, the full
go suite across all workspace modules, and the hygiene tasks. It does
not run `stipulator check`: the verdict executes the accepted test
policy — suite-scale work, race-enabled in the Taskfile, so a CI
seat plausibly more than doubles CI wall on the small runner class —
duplicating the machine-side pre-review gate
(`task check`) that already binds locally. The open question is
whether drift between pushes and the local gate needs a CI-side
tripwire at that cost.

Lands: with cross-tool train chunk 109 (the weekly fleet health sweep
runs check summaries on schedule; its landing decides whether a
CI-side seat adds anything).
