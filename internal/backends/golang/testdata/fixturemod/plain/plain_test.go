package plain

import "testing"

// TestPlain is an ordinary example witness independent of the property-test
// fixtures in other packages.
func TestPlain(t *testing.T) {
	if !Ok() {
		t.Fatal("broken")
	}
}
