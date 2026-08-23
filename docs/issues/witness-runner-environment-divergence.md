# A witness fails only inside the runner — the execution environment diverges from direct `go test`

Field report (tugboat, 2026-08-22): TestDSTComposedFenceSweep (seeds
227/233, godst toolchain) fails ONLY when executed by the witness
runner; four direct `godst test` runs were green, including two
under deliberate machine saturation. The verdict-flipping variable
is therefore something the runner's environment contributes —
inherited env, working directory, process limits, output capture, or
scheduling — not the machine load the direct-saturation control
excluded.

Consequence: a runner-environment-correlated failure reads as a red
requirement and blocks the gate for a defect no direct execution
reproduces; the consumer's only disposition is filing around it.

Fix shape: make the runner's execution environment inspectable (dump
the divergence: env delta, cwd, limits) on a witness whose verdict
disagrees with its cached/prior state, so the correlated variable
can be identified instead of guessed at.

Lands: with the tool-phase stipulator visit, or the next witness
whose verdict flips runner-vs-direct.
