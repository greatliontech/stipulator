package cmd

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/resident"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestExecuteSealsEachEndingWithItsCause pins the CLI's terminal cause
// (REQ-mcp-progress-surfaces's both-surface leg): the one reporter every
// invocation runs under is sealed with the MCP vocabulary — a
// completed run, a failing verdict as a test failure, an operational
// fault as a failure, an interruption as its own cause — and a verdict
// leaves through the exit status, never an in-place exit.
//
//gofresh:pure
func TestExecuteSealsEachEndingWithItsCause(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress-surfaces")
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

// TestExecuteStatesTheResidentSetOnBothLines pins the CLI face's half of
// the resident datum (REQ-mcp-progress-resident's both-surface leg) in process,
// where the overlay can reach it: the CLI's reporter takes the reading,
// so each phase transition's event carries it, the rendered phase line
// carries it as its tail, and the pace line names the peak's phase —
// where the host answers the reading; elsewhere none of the three
// carries it.
func TestExecuteStatesTheResidentSetOnBothLines(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress-resident")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	for key, value := range map[string]string{"NO_COLOR": "1", "GOENV": "off", "GOFLAGS": "", "GOPACKAGESDRIVER": "", "GOTOOLCHAIN": "local"} {
		t.Setenv(key, value)
	}
	saved := chdir
	t.Cleanup(func() { chdir = saved })
	// A passing tree whose check enters every phase: one package with one
	// test, one policy invocation over it, one MAY requirement.
	dir := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":                         "module example.com/residentfix\n\ngo 1.26.4\n",
		"ok/ok.go":                       "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                  "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) { Double(2) }\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n  }\n}\n",
		"specs/check.md":                 "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Any limit in force (an oracle's GOMEMLIMIT) is lifted so the
	// ceiling the lines state is the one THIS operation derived and
	// installed, never one the environment carried in.
	priorLimit := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() { debug.SetMemoryLimit(priorLimit) })
	var status strings.Builder
	var transitions, carrying int
	var phases progress.PhaseTracker
	rendered := progress.Stderr(&status)
	sink := func(e *stipulatorv1.ProgressEvent) {
		if phases.Changed(e) {
			transitions++
			if e.GetResident() != nil {
				carrying++
			}
		}
		rendered(e)
	}
	if err := execute(context.Background(), []string{"-C", dir, "check", "--quiet"}, sink, &status); err != nil {
		t.Fatalf("check: %v\n%s", err, status.String())
	}
	out := status.String()
	if transitions == 0 || !strings.Contains(out, "took ") {
		t.Fatalf("no phase transition or pace line observed:\n%s", out)
	}
	if runtime.GOOS != "linux" {
		if carrying != 0 || strings.Contains(out, "resident") {
			t.Fatalf("a host without the reading stated one:\n%s", out)
		}
		return
	}
	if carrying != transitions {
		t.Fatalf("%d of %d phase transitions carried the reading, want all", carrying, transitions)
	}
	if !strings.Contains(out, " — at the start: resident ") || !strings.Contains(out, "'s exit: resident ") || !strings.Contains(out, "; resident at the end ") || !strings.Contains(out, "; peak ") {
		t.Fatalf("the phase lines or the pace line lack the reading:\n%s", out)
	}
	// The operation runs under the host-derived ceiling, stated with the
	// reading: the limit in force after the operation is the one it
	// installed (the lifted limit above would read as none), and the
	// lines carry exactly it — never a second reading of the host, whose
	// available memory moves between two asks.
	if _, ok := resident.HostMemory(); ok {
		installed := debug.SetMemoryLimit(-1)
		if installed <= 0 || installed == math.MaxInt64 {
			t.Fatalf("the operation installed no ceiling (limit %d)", installed)
		}
		if want := ", ceiling " + resident.ByteWord(uint64(installed)); !strings.Contains(out, want) {
			t.Fatalf("the phase lines lack the ceiling the operation installed (%s):\n%s", want, out)
		}
	}
	// An interrupted run's ending line carries the reading at the end.
	status.Reset()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execute(cancelled, []string{"-C", dir, "check", "--quiet"}, sink, &status); err == nil {
		t.Fatalf("a cancelled run returned no interruption:\n%s", status.String())
	}
	if ending := status.String(); !strings.Contains(ending, "cancelled") || !strings.Contains(ending, " — at the end: resident ") {
		t.Fatalf("the interrupted run's ending lacks the reading at the end:\n%s", ending)
	}
}
