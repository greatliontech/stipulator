package check

import (
	"os"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

// TestMain routes resolver-child re-execs: check.Run reaches the owned
// Go backend, whose client self-execs os.Executable() — this test
// binary, when the run is in-process.
func TestMain(m *testing.M) {
	golang.ResolverChildMain()
	os.Exit(m.Run())
}
