package ok

import "testing"

// The purity assertions let witness derivation publish freshness records
// for these tests; the executor itself ignores them.
//
//gofresh:pure
func TestDouble(t *testing.T) {
	if Double(2) != 4 {
		t.Fatal("2*2 != 4")
	}
	t.Run("zero", func(t *testing.T) {
		if Double(0) != 0 {
			t.Fatal("2*0 != 0")
		}
	})
}

//gofresh:pure
func TestSkipped(t *testing.T) {
	t.Skip("deliberately skipped")
}
