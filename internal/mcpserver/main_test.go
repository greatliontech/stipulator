package mcpserver

import (
	"os"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

// TestMain routes resolver-child re-execs: the server's production
// backends reach the owned Go backend, whose client self-execs
// os.Executable() — this test binary, when the server runs in-process.
func TestMain(m *testing.M) {
	golang.ResolverChildMain()
	os.Exit(m.Run())
}
