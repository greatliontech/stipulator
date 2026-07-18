package redmain

import (
	"os"
	"testing"
)

// TestMain exits red after a green run: every test passes yet the
// package's exit fails.
func TestMain(m *testing.M) {
	m.Run()
	os.Exit(1)
}

func TestGreen(t *testing.T) {}
