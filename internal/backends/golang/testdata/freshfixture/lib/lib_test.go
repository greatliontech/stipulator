package lib

import "testing"

func TestAdd(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-a")
	if Add(1, 2) != 3 || Add(0, 5) != 5 {
		t.Fatal("sum")
	}
}
