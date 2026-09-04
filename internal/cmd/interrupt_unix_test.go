//go:build unix

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
)

// TestInterruptedRunEndsBySignalAfterItsEnding pins the CLI's interrupted
// disposition (REQ-mcp-progress's CLI leg, REQ-policy-cancellation): a
// check interrupted mid-execution renders its ending — the phase it died
// in and what it kept — and then dies by the signal that ended it, so
// its caller observes a signal death, never a verdict's exit status.
func TestInterruptedRunEndsBySignalAfterItsEnding(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress", "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("builds the binary and executes a slow fixture")
	}
	bin := filepath.Join(t.TempDir(), "stipulator")
	build := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("pgrep is how the lingering-child sweep looks; without it the sweep proves nothing")
	}
	dir := t.TempDir()
	// The fixture package is named per run — by this test process's
	// pid — so the lingering-child sweep matches this run's test binary
	// and no other process on the host.
	pkg := fmt.Sprintf("slow%d", os.Getpid())
	for path, content := range map[string]string{
		"go.mod":                         "module example.com/slow\n\ngo 1.26\n",
		pkg + "/slow_test.go":            "package " + pkg + "\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(20 * time.Second) }\n",
		"specs/s.md":                     "# S\n\n**REQ-slow** (behavior): The fixture MUST hold.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"race\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Each signal that ends a run is the one the process dies by: an
	// interactive interrupt and a supervisor's termination read apart.
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		cmd := exec.Command(bin, "-C", dir, "check", "--quiet")
		cmd.Env = append(os.Environ(), "GOENV=off", "GOFLAGS=", "GOPACKAGESDRIVER=", "GOTOOLCHAIN=local", "NO_COLOR=1")
		var stderr syncBuffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// Interrupt once the run is executing.
		deadline := time.Now().Add(60 * time.Second)
		for !strings.Contains(stderr.String(), "phase execution") {
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				t.Fatalf("run never reached execution:\n%s", stderr.String())
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
		err := cmd.Wait()
		ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || !ws.Signaled() || ws.Signal() != sig {
			t.Fatalf("run interrupted by %v ended %v (%v); want death by that signal\n%s", sig, err, cmd.ProcessState, stderr.String())
		}
		if !strings.Contains(stderr.String(), "cancelled in the execution phase; kept nothing") {
			t.Fatalf("run interrupted by %v rendered no ending:\n%s", sig, stderr.String())
		}
		// The cancellation reached the test binary: none of the
		// fixture's processes outlive the run.
		lingering := time.Now().Add(5 * time.Second)
		for {
			out, _ := exec.Command("pgrep", "-f", pkg+".test").Output()
			if len(strings.TrimSpace(string(out))) == 0 {
				break
			}
			if time.Now().After(lingering) {
				t.Fatalf("run interrupted by %v left its test binary running: pids %s", sig, out)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// syncBuffer is a bytes.Buffer the test reads while the child writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
