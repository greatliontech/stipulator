# Chunk-gate campaign for the runner-inspectability folds: priced out, deferred to an idle window

The five-fold change set (e31795d..25b7dcd: timeout attribution, load
attribution, env divergence, claim batching, gap consent) closed its
per-change-set gates on nineteen hand-written `gomutant ephemeral`
probes, each killed by its named test, with five converged adversarial
loops and a green `stipulator check`. The systematic `--changed`
campaign over the same delta measured itself out of the gate window:
63 targets, 4,913 candidates (the spec amendments staled a wide
witness closure), pace-projected ~47h remaining at 2h43m elapsed —
measured while an orphaned campaign from a prior session (gomutant
repo, detached, running since the prior evening) shared the host, so
the quiet-machine cost is lower but the closure size alone keeps it
day-class, far past a gate window. Interrupted gracefully; the
measured prefix (1 target committed, 61 killed, 36 open in-flight)
flushed to the findings document and serves on re-run.

The precedent is the survivor-oracle-narrowing chunk's landing check,
which priced its own window upfront and refused at 3h33m with the gate
riding ephemeral probes — recorded in that chunk's commit. Same
disposition here: probes are the gate evidence; the sweep is
idle-window work, not critical path.

Run: `gomutant run --changed 3b3b1f7` with the standing vouches, on a
quiet machine (`mlock`), resuming the committed prefix. Open survivors
disposition as usual — strengthen the test or attest, never keep.

Lands: first idle window on this host with no concurrent campaign
(machine quiet, load nominal), before the verdict-integrity work
(cross-tool train chunk 141) if one occurs sooner.
