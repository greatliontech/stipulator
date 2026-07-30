# Suite-running MCP tools surfaced no progress to a live agent client

Lands: when the harness MCP server is next restarted against a live agent
client - the remaining arm is purely observational: token presence from a
real Claude Code client and notifications flowing (or their absence
confirmed as the spec's own "only when the client asked" clause). The
notification-blind fallback is no longer pending: every completed
suite-running call now stamps its phase timings as one bounded text line
(REQ-mcp-progress's completed-call fallback), so even a client that saw
no notifications distinguishes slow work from a hang after the fact.
Client patience observed: 120 seconds to backgrounding, hard 600s cap -
any check longer than ten minutes cannot complete through this client
class regardless of emission, so the binding constraint there is suite
wall-time (the serving flywheel), not notifications.

## Observed

REQ-mcp-progress requires bounded progress notifications from
long-running tool calls. In a live agent session (Claude Code MCP
client, tugboat corpus), every suite-running call — `gate`, `check`,
`prune`, and scoped `gate` variants — ran silent until the client's
120-second patience expired and the harness moved the call to a
background task. This happened on more than five calls across the
session; each backgrounding costs the agent a round trip and makes
the tool feel hung. Cause not isolated: the server may not emit the
notifications, or the client may drop them — but the observable
outcome for this client class is "no progress exists", and agent
harnesses are the surface REQ-mcp-server exists for.

## Resolution

Verify notification emission end to end against at least one real
agent client (progress token honored, notifications flowing during
policy execution), and consider a low-cost fallback for clients that
drop notifications: a phase-stamped line in the eventual result
(already partially specified by REQ-mcp-progress's deadline-cause
clause), so even a notification-blind client can distinguish slow
work from a hang after the fact.

## Cause hypothesis to verify first

The server arms the progress seam only when the request carries a
progress token — spec-compliant per REQ-mcp-progress's "only when the
client asked" clause. If this client class never sends tokens, emission
is working as specified and the observable silence is the spec's own
gap: the audit should verify token presence from a real Claude Code
client before touching emission, and if absent, the fix is the
token-independent fallback (bounded heartbeat or phase-stamped result),
not more notifications nobody requested.
