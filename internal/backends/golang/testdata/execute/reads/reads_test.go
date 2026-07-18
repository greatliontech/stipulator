package reads

import (
	"os"
	"testing"
)

// TestReadsFixture reads a committed fixture file so the process's testlog
// records a real runtime input, with a subtest so subtest attribution has
// a second producer to distinguish.
func TestReadsFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/fixture.txt")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("content", func(t *testing.T) {
		t.Log("stipulator:covers REQ-exec-reads-probe")
		if string(b) != "fixture-bytes\n" {
			t.Fatalf("unexpected fixture content %q", b)
		}
	})
}
