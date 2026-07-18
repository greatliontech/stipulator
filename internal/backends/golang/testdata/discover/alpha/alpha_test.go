package alpha

import "testing"

func TestAlpha(t *testing.T) {
	if Greet() != "hi" {
		t.Fatal("greeting changed")
	}
}
