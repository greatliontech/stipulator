package lib

import "testing"

// TestWitPass registers coverage through the raw marker (the fixture
// module cannot depend on the stipulate helper; the marker is the wire
// contract).
func TestWitPass(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-a")
	t.Run("sub", func(t *testing.T) {
		t.Log("stipulator:covers REQ-fix-b")
	})
}

func TestWitFail(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-c")
	t.Error("deliberate failure")
}

func TestWitSkip(t *testing.T) {
	t.Skip("deliberately skipped")
}
