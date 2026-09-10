# The deadline diagnostic's roster head is bounded only by the cap

The terminal-fail diagnostic opens with the reviewed binary bound and
the runtime's running-tests roster, joined whole; the roster grows one
entry per matching dump line with no bound of its own, so a dump whose
roster approaches the failure-output cap starves the residue that
follows it — package output, aborted tests, stderr — of the room the
cap leaves. The runtime lists each running test once, so a roster that
large is a suite that large; no report has shown one.

Lands: a field report of a deadline diagnostic whose roster displaced
its residue.
