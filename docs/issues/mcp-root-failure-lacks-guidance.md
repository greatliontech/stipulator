# MCP root-discovery failure returns a raw open error, not the guided failure

Lands: when the MCP server's root-discovery failure path next changes.

## Observed

Calling the MCP `compile` tool from a working directory with no
`.stipulator/manifest.textproto` in it or any ancestor returns:

```
reading manifest .stipulator/manifest.textproto: open .stipulator/manifest.textproto: no such file or directory
```

The CLI in the identical situation returns the guided failure:

```
stipulator: not inside a stipulator repository (no .stipulator/manifest.textproto in . or any parent); run `stipulator init` to scaffold one
```

## Contract

`REQ-profile-root` requires commands to locate the corpus root by upward
search, "failing with guidance when no ancestor has one," and assigns
discovery to the command surface — which the MCP server is
(`stipulator mcp` inherits its launch working directory). `REQ-mcp-tools`
requires the tools to mirror "the operation semantics exactly." The MCP
failure carries no evidence the upward search ran (the error names a
relative path, not a search) and no pointer to `stipulator init`.

For an agent-facing surface the guidance matters more than on the CLI: a
harness-connected agent hitting the raw error must guess whether the
server is broken, misrooted, or the corpus uninitialized; the CLI's
message answers all three.

## Repro

Connect the MCP server with its working directory outside any corpus and
call `compile`. First observed 2026-07-13 against a freshly connected
server rooted in a repository with no manifest.
