# MCP gap tool takes one requirement where the CLI takes many

Lands: when the MCP `gap` input schema or gap operation semantics next change.

The CLI's `gap --req` is repeatable ("all share the reason and landing
condition"); the MCP `gap` tool's schema takes a single `requirement`
string. `REQ-mcp-tools` says the tools mirror the operation semantics
exactly — the operation evidently supports batch declaration, the MCP
surface does not.

For agent harnesses the asymmetry matters more than for humans: declaring
design-stage gaps for a freshly authored spec is exactly the kind of call
an agent makes over MCP, and one-call-per-requirement multiplies tool
round-trips. Accepting a comma-separated `requirement` (matching
`read_spec`/`context` ids conventions) or an array would close the gap;
whichever spelling lands should compose with a self-landing sentinel if
gap-bulk-cannot-express-self-landing is adopted.
