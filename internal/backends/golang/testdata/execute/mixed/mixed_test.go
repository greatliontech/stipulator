package mixed

import (
	"os"
	"testing"
)

// TestGreen completes and passes; the failing sibling below reds the
// package process around this completed pass.
func TestGreen(t *testing.T) {}

// TestRed fails deliberately after reading an observed input so the mixed
// package exercises process-wide outcome support across a source sibling and
// an observing failure.
func TestRed(t *testing.T) {
	_ = os.Getenv("STIPULATOR_EXEC_MIXED")
	t.Fatal("deliberately red")
}
