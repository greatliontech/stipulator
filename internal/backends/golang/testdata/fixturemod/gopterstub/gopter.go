// Package gopter is a hermetic stub of github.com/leanovate/gopter: the
// classifier resolves the import path and driver names from the type
// checker, so the fixture needs the shapes, not the behavior.
package gopter

import "testing"

// TestParameters mirrors the run configuration.
type TestParameters struct{ MinSuccessfulTests int }

// DefaultTestParameters mirrors the constructor.
func DefaultTestParameters() *TestParameters { return &TestParameters{} }

// Prop mirrors a property under test.
type Prop func() bool

// Properties mirrors the property collection whose check driver
// quantifies.
type Properties struct{ props map[string]Prop }

// NewProperties mirrors the collection constructor.
func NewProperties(params *TestParameters) *Properties {
	return &Properties{props: map[string]Prop{}}
}

// Property mirrors registration: construction alone must not classify
// as a property witness.
func (p *Properties) Property(name string, prop Prop) { p.props[name] = prop }

// TestingRun mirrors the check driver: it runs every property once.
func (p *Properties) TestingRun(t *testing.T) {
	for name, prop := range p.props {
		if !prop() {
			t.Fatalf("property %s falsified", name)
		}
	}
}
