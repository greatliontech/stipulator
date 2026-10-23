package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
)

var (
	cliBinOnce sync.Once
	cliBinDir  string
	cliBinPath string
	cliBinErr  error
)

// cliBinary builds the CLI binary once per test-binary execution and shares the
// compiled artifact across every CLI harness test, preserving each caller's
// fixture directory and process isolation.
func cliBinary(t *testing.T) string {
	t.Helper()
	cliBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "stipulator-cli-test-*")
		if err != nil {
			cliBinErr = err
			return
		}
		cliBinDir = dir
		bin := filepath.Join(dir, "stipulator")
		out, err := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator").CombinedOutput()
		if err != nil {
			cliBinErr = fmt.Errorf("building the CLI: %w\n%s", err, out)
			return
		}
		cliBinPath = bin
	})
	if cliBinErr != nil {
		t.Fatal(cliBinErr)
	}
	return cliBinPath
}

// TestMain routes resolver-child re-execs: commands run in-process by
// tests reach the owned Go backend, whose client self-execs
// os.Executable() — this test binary.
func TestMain(m *testing.M) {
	golang.ResolverChildMain()
	code := m.Run()
	if cliBinDir != "" {
		_ = os.RemoveAll(cliBinDir)
	}
	os.Exit(code)
}
