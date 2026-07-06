package ws

import "testing"

func TestRoot(t *testing.T) {
	if Root() != 1 {
		t.Fatal("broken")
	}
}
