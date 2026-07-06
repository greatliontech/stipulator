package sub

import "testing"

func TestNested(t *testing.T) {
	t.Log("stipulator:covers REQ-ws-a")
	if Nested() != 2 {
		t.Fatal("broken")
	}
}
