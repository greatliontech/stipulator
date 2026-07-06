package extprop_test

import (
	"testing"

	"example.com/fixture/extprop"
	"pgregory.net/rapid"
)

// TestExtProp drives rapid from the external test variant only.
func TestExtProp(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		if !extprop.Ok() {
			rt.Fatal("broken")
		}
	})
}
