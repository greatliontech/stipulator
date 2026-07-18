package killmid

import (
	"os"
	"testing"
)

// TestKilledMidRun reads a fixture — so bytes enter the testlog buffer —
// then dies mid-run: os.Exit skips the testing runtime's testlog flush,
// leaving the capture file empty or truncated exactly like any process
// killed under load.
func TestKilledMidRun(t *testing.T) {
	if _, err := os.ReadFile("testdata/prekill.txt"); err != nil {
		t.Fatal(err)
	}
	os.Exit(2)
}

// TestShadowedByKill would pass, but the sibling above kills the binary
// before this test ever starts: it produces no outcome at all in a
// full-package run.
func TestShadowedByKill(t *testing.T) {}
