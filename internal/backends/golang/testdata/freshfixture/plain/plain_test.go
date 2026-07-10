package plain

import "testing"

func TestPlain(t *testing.T) {
	if !OK() {
		t.Fatal("broken")
	}
}
