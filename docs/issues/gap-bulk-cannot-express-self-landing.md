# Bulk gap declaration cannot express per-requirement self-landing

Lands: when the gap verb surface next changes.

`gap --req` is repeatable, with all requirements sharing the reason and
the landing condition. The natural design-stage idiom — a spec authored
before any code, every requirement knowingly uncovered until implemented —
wants `covered(<the requirement itself>)` per requirement, which the
shared-condition form cannot say.

Observed in a consuming corpus: 22 requirements declared in one call with
`--covered <first-requirement-id>` landed every gap on the first
requirement's coverage; each had to be retargeted with an individual call.
The in-place update path handled the retargeting exactly as
`REQ-gap-verb` promises (changed conditions surfaced, never silent), so
the cost is ergonomic, not correctness — but the mistake is easy to make
and the correct form is 22 invocations.

A `--covered self` sentinel (resolving to each named requirement) would
make the bulk form usable for design-stage corpora. The MCP `gap` tool
would want the same spelling if it adopts bulk input (see
mcp-gap-tool-single-requirement).
