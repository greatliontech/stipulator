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

Reachability (chunk 223's triage, 2026-09-10): NOT reachable at HEAD. Every
spawn through commandContext sits on the parent's paths — the policy
discovery's listings (listPackages, listClosureDirs), the normalizer's
environment query (effectiveGoEnv), the witness runs (runPackage), the
toolchain identity (ToolchainContext) — and the resolver child's request
handlers (ResolveIn, WitnessClassVerdict, NeverServe, Slice, SliceFloor)
reach none of them; the child's package listings run through the
x/tools driver in the child's own process group, which the client's
group kill sweeps. A pin blocking the child at its listing with a
grandchild passes under the kill of the child's group alone, and a
cooperative-ending change set was built and reverted on that evidence.

Remedy sketch, when the trigger fires: the client ends the child
cooperatively — a termination signal the child's root context observes,
so its commandContext cancellations sweep their groups, then the
outright group kill after a bounded grace — with the group sweep owed
after the child's own exit too (the exec package cancels only a live
process).

Lands: a resolver-child request path first spawning through
commandContext — a process in a group of its own, made by the child.
