package plain

import "testing"

// TestPlain always passes and touches nothing outside this package: as a
// bound witness it contributes a rapid-free binary to a witness union
// without ever killing another symbol's mutants.
func TestPlain(t *testing.T) {
	if !Ok() {
		t.Fatal("broken")
	}
}
