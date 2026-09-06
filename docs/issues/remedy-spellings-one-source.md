# Remedy spellings are hand-written in seven places and nothing pins them to the command tree

REQ-change-remediation makes every rendered red name its executable
remedy in the CLI's spelling, and REQ-mcp-guidance keeps the guidance
document bidirectionally checked against the registered knobs — but
the remedy literals themselves (`stipulator pin --req <id>`,
`stipulator unbind --req … --symbol … --clause …`,
`stipulator attest requirement --req <id>`,
`stipulator dispose supersede --from … --into …`, the gap retract and
prune spellings) are composed by hand at seven sites across
`internal/coverage`, `internal/verify`, and `internal/compile`, with
no test parsing them against the real cobra command tree. A renamed
flag or verb would leave a remedy that misleads worse than silence
(REQ-change-remediation's own words), exactly the drift the guidance
check exists to refuse for knobs — one review found the bare word
`force` where the CLI spells `--force`.

Collapse: one remedy-spelling source (a small package composing each
remedy from the verb and flag names the commands register), used by
all seven sites, plus one test that parses every composed remedy
against the command tree — the `policy.Path` precedent
("the document must not become a second, driftable home").

Lands: user decision
