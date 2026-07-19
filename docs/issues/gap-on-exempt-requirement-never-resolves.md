# A gap on an exempt requirement can never resolve

Lands: 5 of the active hot-loop-serving plan (record lifecycle ownership).

## Observed

Gap resolution requires the requirement to reach the covered bucket
(REQ-gap-lifecycle). An unbound MAY requirement renders exempt, never
covered, so a gap declared on it is permanently open: it counts in
`gapsOpen` forever, never becomes due or resolved, and prune can never
touch it. Live instance: tugboat's `REQ-lc-cache-freedom` (a MAY
clause, unbound, exempt) carries a gap tracking its unbuilt production
arm; the record is honest tracking but the lifecycle machinery cannot
ever discharge it, and the standing `gapsOpen` count silently includes
a record with no reachable terminal state.

## Resolution

Either warn at gap write time ("requirement renders exempt under the
active policy; this gap cannot resolve through coverage — prefer a
manual landing condition and expect to fire-and-prune by hand"), or
define resolution for exempt-bucket requirements (e.g. a manual-fired
gap on an exempt requirement resolves on firing alone, since coverage
is not a state the cell can reach).
