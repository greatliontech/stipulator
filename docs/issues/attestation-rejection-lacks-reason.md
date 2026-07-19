# Attestation not admitted by the policy renders as a bare violation

Lands: 7 of the active hot-loop-serving plan (the remediation floor: every
red names its repair).

## Observed

An `attest_requirement` record on a cell whose minimum evidence does
not admit attestation renders as an ordinary violation: the gate shows
the requirement in its red bucket with `attested: 0` and no reason
naming the attestation or why it was not admitted. In a live session
(tugboat, wal conversion) two attestations were authored on
behavior/MUST and wire/MUST cells; the gate reported plain violations,
and the cause — the default policy for those cells demands an executed
witness, so the attestation was silently inadmissible — had to be
inferred from the evidence-model spec. Contrast with the structural
cell's red reason ("needs an analyzer proof (structural)") and the
bind verb's proves-role refusal (which names `stipulate/structural`
and the exact remedy): both route the operator directly to the fix.

## Resolution

When a red requirement holds an attestation record its cell's policy
does not admit, say so in the red reason: "attestation present but
not admitted for (behavior, MUST); minimum evidence is WITNESS".
Optionally, refuse at `attest_requirement` write time when the
requirement's cell can never render the attested bucket — the same
born-valid principle the bind verb already applies to proves-role
claims.
