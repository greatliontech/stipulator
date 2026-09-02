# Serving exemption for random-seeded witnesses follows the direct-call classifier

The freshness contract keeps a random-seeded witness out of serving by
the Go backend's witness classification (REQ-evidence-witness-freshness,
REQ-go-witness-class): a body that directly drives `rapid.Check` /
`rapid.MakeCheck` or gopter's `Properties.TestingRun` is `property`
and random-seeded. The classification is direct-call by contract —
"indirection through a helper does not classify" — because it is the
evidence-ladder claim a bound test makes. Serving asks a different
question: whether the executed quantification draws from a run-time
seed at all, which a helper-indirected driver call (`prop.Run(t, …)`
wrapping `rapid.Check`) does exactly as a direct one, while the
classifier reads it `example` with the near-miss verdict and serving
treats it as deterministic.

Reachable gap: a helper-indirected rapid test passes once, publishes a
record, and serves on every later check — the served-flake shape the
contract closes for the direct form. The direct form is the common
spelling (the field report's witness drove `rapid.Check` directly); the
indirected form is a real one.

Candidate mechanisms, each a design choice the classifier contract
does not make today:

- a transitive seeding class — the subject's source closure reaches a
  recognized driver — resolved from the same recognized-driver table,
  distinct from the evidence classification (which stays direct-call);
- or the evidence classification itself admitting one level of
  in-module helper indirection, which changes what `property`
  evidence means on the ladder.

Lands: user decision.
