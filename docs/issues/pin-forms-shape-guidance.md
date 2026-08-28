# `check` says "re-pin: stipulator pin" but the ids form reports "pins current" while shapes mismatch

Field friction (tugboat, 2026-08-24): `check` failed three
requirements with "shape of <symbol> moved — re-pin after review:
stipulator pin". Running `pin` WITH ids (the natural reading:
re-consent these requirements) answered "pins current" and wrote
nothing — while `verify view=bindings` showed SHAPE_STATE_MISMATCH
on the same rows. Only the blanket no-ids form refreshes shape pins;
the ids form re-pins clause text alone. The interaction cost a full
check cycle (~25 minutes under the caching defect) to a guidance
gap.

Two one-line fixes, both worth landing: `check`'s shape-moved
guidance names the exact invocation (blanket `stipulator pin`, no
ids); and the ids form, when a named requirement's bindings carry a
shape mismatch it is not going to fix, says so instead of "pins
current" — a tool answer that reads as "nothing to do" while the
gate stays red is the defect.

Third fix, same surface (gofresh estate repair review,
2026-08-28): the blanket form rewrites differing SHAPE pins
silently — `records.Pin` (internal/records/pin.go) returns only
preserved content ids, so `pin`'s CLI and MCP surfaces can never
report a moved shape, and `verify`'s ShapeMismatch bucket (the
signal that a bound implementation moved) is cleared invisibly.
Symmetric fix: return the rewritten shape keys and print them
alongside `preserved`.

Lands: folds into cross-tool train chunk 112 (MCP doctrine audit);
the records.Pin shape-rewrite reporting fix rides the same visit —
it changes what the pin surfaces answer, which is that audit's
scope.
