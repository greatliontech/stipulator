package lib

import (
	"io"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate/structural"
	"pgregory.net/rapid"
)

// A body that both asserts structurally and drives the runner: proof on
// the ladder, random-seeded for serving — in either order.
func TestProofThenDrive(t *testing.T) {
	structural.Implements[io.Reader](t, strings.NewReader("x"))
	rapid.Check(t, func(rt *rapid.T) {
		if Add(9, 9) != 18 {
			rt.Fatal("broken")
		}
	})
}

func TestDriveThenProof(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		if Add(9, 9) != 18 {
			rt.Fatal("broken")
		}
	})
	structural.Implements[io.Reader](t, strings.NewReader("x"))
}

// A proof body reaching the runner only through a helper: proof on the
// ladder, refused through the helper.
func TestProofViaHelper(t *testing.T) {
	structural.Implements[io.Reader](t, strings.NewReader("x"))
	runProp(t, func(rt *rapid.T) {
		if Add(9, 9) != 18 {
			rt.Fatal("broken")
		}
	})
}
