package lib

import (
	"testing"

	. "testing/quick"
)

// TestPropQuickDotImported drives the standard library's runner through
// a dot import: the call classifies nothing (only a qualified selector
// does) and the verdict names the dot import, while serving reads the
// bare identifier as the driver it is.
func TestPropQuickDotImported(t *testing.T) {
	if err := Check(func(x int) bool { return Add(x, 0) == x }, nil); err != nil {
		t.Fatal(err)
	}
}
