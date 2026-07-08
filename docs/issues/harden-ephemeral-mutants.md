# harden cannot record one-off manual mutants

Lands: when a consuming repo needs to prove a manual mutation that harden cannot
generate.

The adversarial loop sometimes needs a deterministic mutant outside harden's
current operator set: a generated row drift, a caller-side mapping from unknown
profile data to a missing-fact state, or a review-finding regression that
changes one exact branch. Operators run these by hand: patch the tree, run a
specific test, restore from the staged checkpoint, and remember the result in
conversation or a commit message.

Add an ephemeral mutant runner that accepts a patch plus a test command, applies
the patch in a scratch or staged-checkpoint-backed working tree, requires the
command to fail, restores automatically, and emits a small evidence record. The
record should state the patched file/line, command, observed failure, and the
reason it was outside generated harden scope.

This remains finding evidence, not a new gate input. It standardizes the
break-observe-restore cycle without pretending every hand-authored mutant
belongs in the permanent body-mutation kill sheets.
