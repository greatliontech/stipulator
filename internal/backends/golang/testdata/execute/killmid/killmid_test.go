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
