package outcomes

import "testing"

//gofresh:pure
func TestPass(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-pass")
	t.Run("sub", func(t *testing.T) {
		t.Log("stipulator:covers REQ-fix-sub")
	})
}

//gofresh:pure
func TestFail(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-fail")
	t.Error("deliberate failure")
}

//gofresh:pure
func TestSkip(t *testing.T) {
	t.Skip("deliberately skipped")
}
