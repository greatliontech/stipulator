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

Lands: with the tool-phase stipulator visit, or the next pin-surface
change.
