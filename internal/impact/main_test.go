package impact

import (
	"os"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

// TestMain routes resolver-child re-execs: Preview reaches the owned Go
// backend, whose client self-execs os.Executable() — this test binary.
func TestMain(m *testing.M) {
	golang.ResolverChildMain()
	os.Exit(m.Run())
}
