package sub

import "testing"

func TestSub(t *testing.T) {
	if One() != 1 {
		t.Fatal("one drifted")
	}
}
