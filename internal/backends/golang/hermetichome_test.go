package golang

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// hermeticGoHome returns a temp home for a toolchain child, with the
// toolchain's telemetry mode set off in the one place the toolchain
// reads it — the mode file under the home's config directory (no
// environment variable sets it). With telemetry on, the toolchain
// writes counter files under the home and forks a detached upload
// sidecar that outlives the child and races the home's removal at test
// end; with it off, nothing writes under the home but the child's own
// work.
func hermeticGoHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	telemetryDir := filepath.Join(home, ".config", "go", "telemetry")
	if err := os.MkdirAll(telemetryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(telemetryDir, "mode"), []byte("off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestHermeticGoHomeKeepsTheToolchainFromWritingUnderIt pins the helper
// every temp-home spawn in this package relies on: a toolchain child
// run under the hermetic home leaves nothing there but the mode file —
// no counter files, no detached writer — so the home is removable the
// moment the test ends, under any load.
func TestHermeticGoHomeKeepsTheToolchainFromWritingUnderIt(t *testing.T) {
	home := hermeticGoHome(t)
	cmd := exec.Command("go", "env", "GOROOT", "GOMODCACHE", "GOCACHE")
	cmd.Env = []string{"HOME=" + home, "GOENV=off", "PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go env under the hermetic home: %v\n%s", err, out)
	}
	var found []string
	if err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(home, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0] != ".config/go/telemetry/mode" {
		t.Fatalf("the toolchain wrote under the hermetic home: %v", found)
	}
}
