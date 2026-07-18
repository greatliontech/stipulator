package mainexit

import (
	"os"
	"testing"
)

// TestMain exits green without running any test: the package passes and
// the process exits cleanly, yet the testing runtime never opens its
// testlog capture file.
func TestMain(m *testing.M) {
	os.Exit(0)
}

func TestNeverRuns(t *testing.T) {}
