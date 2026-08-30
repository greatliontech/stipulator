# CLI bind silently keeps only the last claim when flags repeat

Field friction (gomutant train chunk 113, 2026-08-30): `stipulator
bind --req A --role tests --symbol X --req B --role tests --symbol Y`
exits 0 and writes exactly one claim — pflag string flags overwrite on
repetition, so every claim but the last is silently dropped. The same
holds for a repeated `--symbol` under one `--req`. Two invocation
shapes a caller would reasonably read as a batch (the MCP `bind` IS
batch, all-or-nothing) instead succeed while discarding work; the
caller discovers the loss only by re-reading the bindings file.

Fix direction: make the CLI either accept the batch (repeated flag
groups building a claims list, all-or-nothing like the MCP face) or
refuse repetition loudly — never accept-and-drop. Silent last-wins on
a write command fails the same honesty bar as a silent coverage cap.

Lands: with train chunk 114.
