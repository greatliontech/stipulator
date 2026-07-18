package mixed

import "testing"

// TestGreen completes and passes; the failing sibling below reds the
// package process around this completed pass.
func TestGreen(t *testing.T) {}

// TestRed fails deliberately.
func TestRed(t *testing.T) {
	t.Fatal("deliberately red")
}
