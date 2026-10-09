# Discovery exit 137 and long memory-targeted checks

Lands: user decision

Field report from bldc on 2026-10-09. The move feature and its reviewed
corrections are recoverable at bldc `1c2e7bc` (parent `14e743d`). The initial
killed invocation preceded the later projection/publication corrections; the
landing identifies the change set and final successful run, not exact source
bytes for the initial failure.

After the uncached Go/root suite and 217 web tests passed, the canonical command
was run with its ordinary environment:

```sh
mlock run task check
```

Formatting, vet, generated-code comparison, web type checking and web tests
completed. Stipulator reported compile exit with resident 1.4 GiB, peak 2.4 GiB
and ceiling 2.7 GiB, then entered discovery at elapsed 59.2s. The task runner
subsequently reported exit 137 with no Stipulator verdict. The phase timestamp
does not measure how long discovery ran before termination. No kernel/service
evidence established the killer or cause; this is not a diagnosed OOM and was
not counted as passing verification.

A retry used only a Go runtime memory target, preserving policy and test depth:

```sh
mlock run env GOMEMLIMIT=1GiB task check
```

It completed in 1h33m15.8s: discovery 1h11m26.3s, execution 21m40s, reported
discovery-exit peak 6.7 GiB. Witness execution completed, but the verdict failed
an independently explained stale editorial requirement pin. After re-consent,
the same command passed in 12m6.6s. The later check of the reviewed corrections
passed in 1h22m35.4s: compile 54.5s, discovery 1h2m31s, execution 19m7s,
verification 2.8s, reported discovery-exit peak 6.8 GiB.

`GOMEMLIMIT` is not a hard process-tree ceiling. These observations neither
establish that the target was ignored nor isolate its effect from host load,
cache state or evidence reuse. No test counts, execution deadlines or resource
protections were weakened.

The installed binary inspected afterward was v0.73.6 at `c01a298`, built with
Go 1.27.1 and gofresh v0.112.1. The required upstream pull reached `0d7b3b6`;
there is no claim that the newer upstream source reproduced the observation.
The export-cache, completed-package-peak and resolver-resident-set reports
remain indexed; the closed origin-diagnostic report is not reopened here.

Investigate whether the discovery boundary can give an actionable resource
account before an external kill and whether its current memory/throughput
behavior under a constrained runtime target can be reproduced on current code.
Distinguish parent, resolver and child-process measurements. Preserve honest
incomplete verdicts and host protection; the tool owner sequences the work.
