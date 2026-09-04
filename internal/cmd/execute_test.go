package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestExecuteSealsEachEndingWithItsCause pins the CLI's terminal cause
// (REQ-mcp-progress's both-surface leg): the one reporter every
// invocation runs under is sealed with the MCP vocabulary — a
// completed run, a failing verdict as a test failure, an operational
// fault as a failure, an interruption as its own cause — and a verdict
// leaves through the exit status, never an in-place exit.
//
//gofresh:pure
func TestExecuteSealsEachEndingWithItsCause(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	t.Setenv("NO_COLOR", "1")
	saved := chdir
	t.Cleanup(func() { chdir = saved })
	dir := t.TempDir()
	for path, content := range map[string]string{
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		"specs/a.md":                     "# A\n\n**REQ-a** (behavior): The fixture MUST hold.\n\n**REQ-a** (behavior): The identifier repeats.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var status strings.Builder
	sealedCause := func(ctx context.Context, args ...string) (stipulatorv1.TerminalCause, error) {
		t.Helper()
		var last *stipulatorv1.ProgressEvent
		err := execute(ctx, args, func(e *stipulatorv1.ProgressEvent) { last = e }, &status)
		if last == nil {
			t.Fatalf("%v: no terminal event", args)
		}
		return last.GetTerminalCause(), err
	}
	// A compile with a duplicate identifier is a failing verdict: exit
	// status 1, sealed as a test failure.
	cause, err := sealedCause(context.Background(), "-C", dir, "compile")
	var exit ExitStatus
	if cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE || !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("failing compile sealed %v with %v; want a test failure exiting 1", cause, err)
	}
	if cause, err := sealedCause(context.Background(), "-C", dir, "no-such-verb"); cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE || err == nil || errors.As(err, &exit) {
		t.Fatalf("unknown verb sealed %v with %v; want a failure without an exit status", cause, err)
	}
	if cause, err := sealedCause(context.Background(), "-C", dir, "guidance", "check"); cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED || err != nil {
		t.Fatalf("guidance sealed %v with %v; want completed", cause, err)
	}
	// An interrupted run returns the interruption, never the verdict a
	// verb produced under the dying context.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var interrupted Interrupted
	if cause, err := sealedCause(cancelled, "-C", dir, "compile"); cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED || !errors.As(err, &interrupted) || errors.As(err, &exit) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run sealed %v with %v; want cancelled, returned as the interruption", cause, err)
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if cause, err := sealedCause(expired, "-C", dir, "compile"); cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE || !errors.As(err, &interrupted) || errors.As(err, &exit) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired run sealed %v with %v; want the deadline, returned as the interruption", cause, err)
	}
	if status.Len() != 0 {
		t.Fatalf("verbs that enter no phase wrote a pace line: %q", status.String())
	}
}
