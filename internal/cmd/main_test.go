package cmd

import (
	"os"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

// TestMain routes resolver-child re-execs: commands run in-process by
// tests reach the owned Go backend, whose client self-execs
// os.Executable() — this test binary.
func TestMain(m *testing.M) {
	golang.ResolverChildMain()
	os.Exit(m.Run())
}
