//go:build unix

package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/stipulate"
)

// writeCheckTree lays out a temporary corpus tree whose accepted policy
// executes one deliberately slow package, so a deadline or cancellation
// lands during policy execution.
func writeCheckTree(t *testing.T, slowTest string) string {
	t.Helper()
	files := map[string]string{
		"go.mod":                         "module example.com/mcpfix\n\ngo 1.26.4\n",
		"slow/slow_test.go":              slowTest,
		"specs/check.md":                 "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n  }\n}\n",
	}
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// neutralAmbient pins the ambient controls policy normalization reads to
// a known hermetic state, so host configuration cannot steer these tests.
func neutralAmbient(t *testing.T) {
	t.Helper()
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("GOTOOLCHAIN", "local")
}

// TestCheckToolDeadlineNamesExpiredPhaseAndCause pins deadline
// attribution through the server's check handler: a short deadline over a
// tree whose policy execution outlasts it yields an error naming the
// execution phase and the deadline as the terminal cause, with the
// context error preserved for programmatic dispatch.
func TestCheckToolDeadlineNamesExpiredPhaseAndCause(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := writeCheckTree(t, "package slow\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n"+
		"func TestSleeps(t *testing.T) {\n\ttime.Sleep(time.Minute)\n}\n")
	s := New(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "check"}}
	_, _, err := s.toolCheck(ctx, req, struct{}{})
	if err == nil {
		t.Fatal("deadline-terminated check returned no error")
	}
	if !strings.Contains(err.Error(), "deadline expired in the execution phase") {
		t.Errorf("error does not name the expired phase and cause: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("context cause lost: %v", err)
	}
}

// TestCheckToolClientCancellationKillsEveryChildProcess proves
// cancellation end to end at the MCP surface: a client cancelling its
// check call over the wire terminates the executing test binary — a
// grandchild of the server — and the call returns promptly with the
// cancellation, leaving no orphan process behind.
func TestCheckToolClientCancellationKillsEveryChildProcess(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-cancellation", "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	// The sleeping test publishes its own pid before blocking: the pid of
	// the test binary grandchild, exactly the process an orphaning bug
	// would leave running.
	dir := writeCheckTree(t, "package slow\n\nimport (\n\t\"os\"\n\t\"strconv\"\n\t\"testing\"\n\t\"time\"\n)\n\n"+
		"func TestSleeps(t *testing.T) {\n"+
		"\tif err := os.WriteFile(\"pid.txt\", []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {\n\t\tt.Fatal(err)\n\t}\n"+
		"\ttime.Sleep(time.Minute)\n}\n")

	s := New(dir)
	ct, st := mcp.NewInMemoryTransports()
	serverCtx, stopServer := context.WithCancel(context.Background())
	t.Cleanup(stopServer)
	go func() { _ = s.MCP().Run(serverCtx, st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}})
		done <- err
	}()

	pidFile := filepath.Join(dir, "slow", "pid.txt")
	var pid int
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil && len(b) > 0 {
			pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatalf("parse test binary pid: %v", err)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("check returned before the fixture test started: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if pid == 0 {
		t.Fatal("fixture test binary never started")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled call returned %v, want context canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled check did not abort promptly")
	}

	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("test binary %d survived client cancellation", pid)
}
