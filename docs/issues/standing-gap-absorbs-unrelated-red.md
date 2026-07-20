# A standing gap absorbs a later red of an unrelated class

Lands: when gap records gain violation-class scoping.

## Observed

The gap lifecycle lets a covered requirement carry an open gap with an
unfired manual condition — a declared violation that outlives green
witnesses. That creates the first steady-state path where a green
requirement holds a standing declaration: if the requirement later goes
red for a different reason (an undispositioned text edit staling its
bindings, a broken symbol), the gate sees a red requirement named by a
gap and raises no violation — but the gap's reason describes the
original violation class, not the new red. The gate's per-requirement
model has always read "a gap is a gap"; before standing gaps existed,
every gap was declared while its requirement was already red, so the
reason and the red matched by construction. Witnessed regressions are
still caught through suite health; the blind spot is auditability — a
reader trusting the gap's reason misattributes the red.

## Direction

Class-scoped gaps (a gap declares which bucket transitions it excuses)
would close it; so would a lifecycle rule surfacing a bucket transition
on a requirement with a standing gap as its own finding. Both are gap
model changes beyond a records edit.
