package panicky

import "testing"

func TestPanics(t *testing.T) {
	panic("deliberate abort")
}

func TestShadowed(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-shadow")
}
