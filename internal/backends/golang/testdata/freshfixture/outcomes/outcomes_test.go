package outcomes

import "testing"

func TestPass(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-pass")
	t.Run("sub", func(t *testing.T) {
		t.Log("stipulator:covers REQ-fix-sub")
	})
}

func TestFail(t *testing.T) {
	t.Log("stipulator:covers REQ-fix-fail")
	t.Error("deliberate failure")
}

func TestSkip(t *testing.T) {
	t.Skip("deliberately skipped")
}
