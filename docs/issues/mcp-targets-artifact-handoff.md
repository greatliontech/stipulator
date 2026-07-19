# MCP targets export has no artifact handoff for gomutant

Lands: when the MCP `targets` export surface next changes, or when MCP clients
can pass typed tool-result artifacts directly between tools.

## Observed

The Stipulator-to-gomutant handoff worked in a consuming repository:
`stipulator_targets` emitted `stipulator.binding-surfaces/v1`, and
`gomutant_discover` / `gomutant_run` accepted the same structure through
`targets_json`.

The MCP workflow still required copying the entire JSON result from one tool call
into another. Even a two-symbol scoped surface produced enough nested binding data
to make the inline call fragile. A full repository surface is much larger and is
likely to hit client truncation or manual quoting mistakes.

## Resolution

Expose a handoff artifact for binding surfaces. Possible forms:

- `stipulator_targets` can write the export to a caller-named file path;
- `stipulator_targets` can return an MCP resource URI that another tool can
  consume;
- a small companion tool can materialize the last target report as a targets
  document.

The file/resource content should be exactly the existing
`stipulator.binding-surfaces/v1` structure so gomutant does not need a new input
format.
