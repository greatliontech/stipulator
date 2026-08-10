package lib

import (
	"testing"

	. "pgregory.net/rapid"
)

// TestPropDotImported drives the runner through a dot import: the call
// classifies nothing (only a qualified selector does), and the verdict
// names the dot import.
func TestPropDotImported(t *testing.T) {
	Check(t, func(rt *T) {
		if Add(1, 1) != 2 {
			rt.Fatal("broken")
		}
	})
}
