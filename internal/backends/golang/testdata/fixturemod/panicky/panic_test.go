package panicky

import "testing"

// TestPanics aborts the package's test binary; TestShadowed never runs
// and must produce no outcome event.
func TestPanics(t *testing.T) {
	panic("deliberate abort")
}

func TestShadowed(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-shadow")
}
