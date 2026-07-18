package ok

import "testing"

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

func TestSkipped(t *testing.T) {
	t.Skip("deliberately skipped")
}
