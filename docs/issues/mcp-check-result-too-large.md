# MCP check returns a full result too large for the client response

Lands: when the MCP `check` result schema or rendering next changes.

## Observed

While migrating a consuming repository to a Stipulator corpus, `stipulator_check`
passed but returned one large JSON line. The MCP client truncated the response and
saved the full payload to an implementation-specific tool-output file. The user
visible result did not show the verdict, gate status, gap count, or binding
summary without a second manual inspection step using that saved path.

The same workflow had already used `gate view=summary` and `verify view=summary`
successfully. `check` is the operation that should answer whether the tree
passes, but its MCP face made the pass/fail answer less accessible than the
component commands.

## Resolution

Give the MCP `check` surface a compact summary mode, or make the default MCP
response start with a small stable verdict summary before optional detail. The
summary should include at least:

- `passed`;
- suite health;
- binding verification counts;
- gate status and violation count;
- open and prunable gap counts.

The full `CheckResult` can remain available, but an agent-facing check call
needs the one-verdict answer without relying on client truncation side channels.
