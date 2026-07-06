# gopter-driven tests classify as example witnesses

Lands: when a corpus standardized on gopter needs invariant coverage.

The witness classifier recognizes two property-witness shapes: native fuzz
targets (`*testing.F`) and `pgregory.net/rapid` check drivers
(`rapid.Check` / `rapid.MakeCheck`), per REQ-go-witness-class. A
generator-driven test written with `github.com/leanovate/gopter`
(`properties.TestingRun(t)`) still classifies as an example witness, so it
cannot cover an `invariant` requirement.

Extension is mechanical once needed: the classifier's body inspection
(`WitnessClass`, `internal/backends/golang/golang.go`) keys driver calls by
package path and name; gopter's driver is
`gopter.Properties.TestingRun` / `prop.ForAll` fed through
`properties.Property`. The deliberate rule to preserve: generator
construction alone does not quantify — only the check driver classifies.
