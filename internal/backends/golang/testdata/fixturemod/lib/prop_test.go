package lib

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

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

// TestPropTwoDrivers drives the runner twice: the first call is the
// driving site.
func TestPropTwoDrivers(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		if Add(1, 1) != 2 {
			rt.Fatal("broken")
		}
	})
	rapid.Check(t, func(rt *rapid.T) {
		if Add(2, 1) != 3 {
			rt.Fatal("broken")
		}
	})
}

// TestPropQuickCheck drives the standard library's property runner: a
// property witness, random-seeded from the wall clock.
func TestPropQuickCheck(t *testing.T) {
	if err := quick.Check(func(x int) bool { return Add(x, 0) == x }, nil); err != nil {
		t.Fatal(err)
	}
}

// TestPropQuickCheckEqual drives the runner's equality form over a
// configuration carrying its own source: classified the same.
func TestPropQuickCheckEqual(t *testing.T) {
	cfg := &quick.Config{Rand: rand.New(rand.NewSource(1))}
	if err := quick.CheckEqual(func(x int) int { return Add(x, 1) }, func(x int) int { return x + 1 }, cfg); err != nil {
		t.Fatal(err)
	}
}

// TestQuickValueOnly generates with quick.Value and drives nothing:
// the near-miss names the driver it lacks.
func TestQuickValueOnly(t *testing.T) {
	if _, ok := quick.Value(reflect.TypeOf(0), rand.New(rand.NewSource(1))); !ok {
		t.Fatal("no value")
	}
}
