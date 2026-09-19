# Impure sibling tests in one package never earn an observation proof

Two tests in one package that both read runtime inputs and carry no
author's purity assertion share one process on both forms that reach
them: the whole-package process at discovery and the selective process
over the package's stale names (the drift retry never reaches them —
never published means never served, so never drifted). A shared
process is never a proof candidate (the proof needs the process to run
its subject alone), and an unproven observing record's post-run
validation can never return valid, so the record is dropped and
counted uncacheable: neither test ever publishes and both re-execute
every run — the outcome REQ-evidence-witness-freshness states for an
unasserted impure test. What the spec does not state is whether the
selective form should ever narrow to one test to earn a proof: the
isolation pass already spawns solo processes with owned observations,
but only for tests a red or aborted process denied. Spending one extra
process per impure subject per run to earn proofs is a cost-and-scope
call.

Lands: user decision
