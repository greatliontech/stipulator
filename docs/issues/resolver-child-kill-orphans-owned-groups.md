# A resolver child killed outright by its client orphans its group-isolated spawns

REQ-go-owned-processes requires every Go child's descendant tree to
terminate with the operation's cancellation, and commandContext meets it
in-process: each spawn runs in its own process group and the context's
cancellation kills the group. The served resolver child is itself such a
spawn, and its client cancels it by killing the child's group outright,
so the child's own cancellation handlers never run: every spawn the
child made through commandContext — package listings, discovery, test
runs, each in its own group — outlives the kill. The descendants test
(TestGoResolverCancellationTerminatesDescendants) sees only the fixture's
first spawn, which at HEAD is the descendant-free provenance probe kept
in the caller's group for this reason; a fixture blocking the child at a
later, group-isolated spawn reproduces the orphan.

Remedy sketch: the client cancels the child cooperatively — a
termination signal the child's root context observes, so its
commandContext cancellations sweep their groups, then the outright
group kill after a bounded grace — or the child marks each spawn to die
with it (Pdeathsig on linux, one level only).

Lands: cross-tool train chunk 223 (one witness pipeline —
REQ-policy-cancellation's invariants are its charter).
