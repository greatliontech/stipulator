package lib

import (
	"testing"

	"pgregory.net/rapid"
)

// TestPropRapidCheck drives the rapid check runner: a property witness.
func TestPropRapidCheck(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		if Add(1, 2) != 3 {
			rt.Fatal("broken")
		}
	})
}

// TestPropRapidMakeCheck drives the subtest-shaped runner: also a
// property witness.
func TestPropRapidMakeCheck(t *testing.T) {
	t.Run("prop", rapid.MakeCheck(func(rt *rapid.T) {
		if Add(2, 2) != 4 {
			rt.Fatal("broken")
		}
	}))
}

// TestPropRapidGeneratorOnly constructs a generator but never drives a
// check runner: quantifying over nothing, it stays an example witness.
func TestPropRapidGeneratorOnly(t *testing.T) {
	if got := rapid.Int(); got != Add(got, 0) {
		t.Fatal("broken")
	}
}
