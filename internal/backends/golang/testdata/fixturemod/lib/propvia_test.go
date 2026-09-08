package lib

import (
	"testing"

	"example.com/fixture/badhelper"
	"example.com/fixture/helpers"
	"pgregory.net/rapid"
)

// runner reaches the driver through a method on an instantiated
// generic receiver.
type runner[T any] struct{}

func (runner[T]) Run(t *testing.T, body func(*rapid.T)) { rapid.Check(t, body) }

// runProp wraps the rapid driver: a helper-indirected property test
// classifies example on the evidence ladder and is random-seeded for
// serving.
func runProp(t *testing.T, body func(*rapid.T)) {
	rapid.Check(t, body)
}

// runPropTwice reaches the driver through a second hop.
func runPropTwice(t *testing.T, body func(*rapid.T)) {
	runProp(t, body)
}

type propRunner struct{}

// Run reaches the driver through a method helper.
func (propRunner) Run(t *testing.T, body func(*rapid.T)) {
	rapid.Check(t, body)
}

// spin calls itself before the driver: the walk survives the cycle.
func spin(t *testing.T, n int, body func(*rapid.T)) {
	if n > 0 {
		spin(t, n-1, body)
		return
	}
	rapid.Check(t, body)
}

// plainHelper reaches no driver.
func plainHelper(t *testing.T) int { return Add(1, 1) }

// ping and pong cycle without ever reaching a driver: the walk must
// terminate on the cycle and the test serves.
func ping(n int) int {
	if n <= 0 {
		return 0
	}
	return pong(n - 1)
}

func pong(n int) int { return ping(n) }

func TestPropViaHelper(t *testing.T) {
	runProp(t, func(rt *rapid.T) {
		if Add(1, 2) != 3 {
			rt.Fatal("broken")
		}
	})
}

func TestPropViaTwoHops(t *testing.T) {
	runPropTwice(t, func(rt *rapid.T) {
		if Add(2, 2) != 4 {
			rt.Fatal("broken")
		}
	})
}

func TestPropViaMethod(t *testing.T) {
	propRunner{}.Run(t, func(rt *rapid.T) {
		if Add(3, 3) != 6 {
			rt.Fatal("broken")
		}
	})
}

func TestPropViaCycle(t *testing.T) {
	spin(t, 2, func(rt *rapid.T) {
		if Add(4, 4) != 8 {
			rt.Fatal("broken")
		}
	})
}

func TestPlainViaHelper(t *testing.T) {
	if plainHelper(t) != 2 {
		t.Fatal("broken")
	}
}

func TestPlainViaCycle(t *testing.T) {
	if ping(3) != 0 {
		t.Fatal("broken")
	}
}

func TestPropViaOtherPackage(t *testing.T) {
	helpers.Run(t, func(rt *rapid.T) {
		if Add(5, 5) != 10 {
			rt.Fatal("broken")
		}
	})
}

func TestPropViaGenericMethod(t *testing.T) {
	runner[int]{}.Run(t, func(rt *rapid.T) {
		if Add(6, 6) != 12 {
			rt.Fatal("broken")
		}
	})
}

func TestPropViaBadHelper(t *testing.T) {
	badhelper.Run(t, func(rt *rapid.T) {
		if Add(8, 8) != 16 {
			rt.Fatal("broken")
		}
	})
}

func TestPropViaDependencyHelper(t *testing.T) {
	rapid.Drive(t, func(rt *rapid.T) {
		if Add(7, 7) != 14 {
			rt.Fatal("broken")
		}
	})
}
