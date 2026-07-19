package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// notificationLog captures progress notifications a client session
// receives, safely across the transport's dispatch goroutines.
type notificationLog struct {
	mu     sync.Mutex
	params []*mcp.ProgressNotificationParams
}

func (l *notificationLog) add(p *mcp.ProgressNotificationParams) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.params = append(l.params, p)
}

func (l *notificationLog) snapshot() []*mcp.ProgressNotificationParams {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*mcp.ProgressNotificationParams(nil), l.params...)
}

// checkHarness builds a session against a server whose check operation is
// injected, with the client capturing progress notifications.
func checkHarness(t *testing.T, runCheck func(context.Context, bool) (*stipulatorv1.CheckResult, error)) (*mcp.ClientSession, *notificationLog) {
	t.Helper()
	s := &Server{
		fsys: func() fs.FS {
			return fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte(doc)},
			}
		},
		runCheck: runCheck,
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(context.Background(), st) }()
	log := &notificationLog{}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			log.add(req.Params)
		},
	})
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, log
}

// fixtureResult loads the published check-result wire fixture — the
// full-field message MCP and CLI consumers are promised.
func fixtureResult(t *testing.T) *stipulatorv1.CheckResult {
	t.Helper()
	b, err := os.ReadFile("../policy/testdata/check_result.json")
	if err != nil {
		t.Fatal(err)
	}
	res := &stipulatorv1.CheckResult{}
	if err := protojson.Unmarshal(b, res); err != nil {
		t.Fatal(err)
	}
	return res
}

// TestCheckToolStructuredResultMirrorsCheckResult validates the tool's
// output against the protobuf result: the structured content of a check
// call over the full-field wire fixture strict-round-trips into an equal
// CheckResult — every key is a CheckResult field, every field survives.
func TestCheckToolStructuredResultMirrorsCheckResult(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-report-check-result")
	want := fixtureResult(t)
	sess, _ := checkHarness(t, func(context.Context, bool) (*stipulatorv1.CheckResult, error) {
		return want, nil
	})
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("check tool errored: %v", res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	got := &stipulatorv1.CheckResult{}
	// Strict decoding: an unknown key — progress smuggled into the
	// payload, a drifted field name — fails the round trip.
	if err := protojson.Unmarshal(b, got); err != nil {
		t.Fatalf("structured content is not a strict CheckResult: %v\n%s", err, b)
	}
	if !proto.Equal(want, got) {
		t.Errorf("structured content decodes to a different message:\n%s", b)
	}
}

// TestCheckToolFailingTreeIsSuccessfulCall pins the error split: a tree
// failing the check is a successful call carrying passed=false — with the
// terminal progress event naming the test failure, so a red suite is
// distinguishable from every operational ending — and the tool error
// channel is reserved for operational faults.
func TestCheckToolFailingTreeIsSuccessfulCall(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-tools", "REQ-mcp-progress")
	redInv := &stipulatorv1.InvocationHealth{}
	redInv.SetInvocation("race")
	redInv.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	redExecution := &stipulatorv1.ExecutionReport{}
	redExecution.SetInvocations([]*stipulatorv1.InvocationHealth{redInv})
	failing := &stipulatorv1.CheckResult{}
	failing.SetPassed(false)
	failing.SetTestsExecuted(1)
	failing.SetExecution(redExecution)
	sess, log := checkHarness(t, func(context.Context, bool) (*stipulatorv1.CheckResult, error) {
		return failing, nil
	})
	params := &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}}
	params.SetProgressToken("fail-progress")
	res, err := sess.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("failing tree surfaced as a tool error: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	got := &stipulatorv1.CheckResult{}
	if err := protojson.Unmarshal(b, got); err != nil {
		t.Fatal(err)
	}
	if got.GetPassed() {
		t.Errorf("failing verdict lost on the wire: %s", b)
	}
	notes := waitNotifications(t, log, 1)
	final := &stipulatorv1.ProgressEvent{}
	if err := protojson.Unmarshal([]byte(notes[len(notes)-1].Message), final); err != nil {
		t.Fatal(err)
	}
	if final.GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE {
		t.Errorf("terminal cause = %v, want TEST_FAILURE for a red suite", final.GetTerminalCause())
	}

	opSess, _ := checkHarness(t, func(context.Context, bool) (*stipulatorv1.CheckResult, error) {
		return nil, errors.New("policy record unreadable: permission denied")
	})
	res, err = opSess.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("operational fault did not surface as a tool error")
	}
}

// TestCheckToolRefusesViewAndScopeInputs pins the check tool's input
// surface: it answers as its one structured result message and carries no
// views or scopes — a view or scope argument is refused, never silently
// ignored.
func TestCheckToolRefusesViewAndScopeInputs(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-views")
	sess, _ := checkHarness(t, func(context.Context, bool) (*stipulatorv1.CheckResult, error) {
		return &stipulatorv1.CheckResult{}, nil
	})
	for _, args := range []map[string]any{
		{"view": "summary"},
		{"ids": "REQ-m-a"},
		{"bucket": "uncovered"},
		{"filter": "REQ-*"},
		{"path": "specs"},
	} {
		res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: args})
		if err == nil && !res.IsError {
			t.Errorf("check accepted a view/scope input %v", args)
		}
	}
}

// waitNotifications polls until the log holds at least n notifications.
func waitNotifications(t *testing.T, log *notificationLog, n int) []*mcp.ProgressNotificationParams {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := log.snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d progress notifications; have %d", n, len(log.snapshot()))
	return nil
}

// TestCheckToolProgressRidesNotificationsNotPayload pins the progress
// channel: a call carrying a progress token receives bounded phase and
// per-invocation events — elapsed time and counts included — as MCP
// progress notifications, the terminal event names the cause, and the
// result payload carries none of it; a call without a token receives
// nothing.
func TestCheckToolProgressRidesNotificationsNotPayload(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	passing := &stipulatorv1.CheckResult{}
	passing.SetPassed(true)
	sess, log := checkHarness(t, func(ctx context.Context, _ bool) (*stipulatorv1.CheckResult, error) {
		rep := progress.FromContext(ctx)
		rep.Phase(stipulatorv1.Phase_PHASE_COMPILE)
		rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
		rep.Step("race", 1, 2)
		rep.Step("race", 2, 2)
		return passing, nil
	})
	params := &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}}
	params.SetProgressToken("check-progress")
	res, err := sess.CallTool(context.Background(), params)
	if err != nil || res.IsError {
		t.Fatalf("check: %v %v", err, res)
	}

	// Bounded: two phase transitions, the invocation-completion milestone,
	// and the terminal event — the intermediate step is rate-limited away.
	notes := waitNotifications(t, log, 4)
	if len(notes) != 4 {
		t.Fatalf("got %d notifications, want 4 (compile, execution, race 2/2, terminal): %v", len(notes), notes)
	}
	var prev float64
	var events []*stipulatorv1.ProgressEvent
	for i, n := range notes {
		if n.ProgressToken != "check-progress" {
			t.Errorf("notification %d token = %v", i, n.ProgressToken)
		}
		if n.Progress <= prev {
			t.Errorf("progress value not increasing: %v then %v", prev, n.Progress)
		}
		prev = n.Progress
		e := &stipulatorv1.ProgressEvent{}
		if err := protojson.Unmarshal([]byte(n.Message), e); err != nil {
			t.Fatalf("notification %d message is not a ProgressEvent: %v\n%s", i, err, n.Message)
		}
		if e.GetElapsed() == nil {
			t.Errorf("notification %d carries no elapsed time: %s", i, n.Message)
		}
		events = append(events, e)
	}
	if events[0].GetPhase() != stipulatorv1.Phase_PHASE_COMPILE ||
		events[1].GetPhase() != stipulatorv1.Phase_PHASE_EXECUTION {
		t.Errorf("phase transitions wrong: %v", events)
	}
	if events[2].GetInvocation() != "race" || events[2].GetCompleted() != 2 || events[2].GetTotal() != 2 {
		t.Errorf("invocation milestone = %v, want race 2/2", events[2])
	}
	final := events[3]
	if final.GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED {
		t.Errorf("terminal cause = %v, want COMPLETED", final.GetTerminalCause())
	}
	for _, e := range events[:3] {
		if e.GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED {
			t.Errorf("non-terminal event carries a cause: %v", e)
		}
	}

	// Progress stays off the result payload: the structured content is a
	// strict CheckResult and no progress vocabulary leaks into it.
	b, _ := json.Marshal(res.StructuredContent)
	if err := protojson.Unmarshal(b, &stipulatorv1.CheckResult{}); err != nil {
		t.Fatalf("result payload is not a strict CheckResult: %v\n%s", err, b)
	}
	for _, leak := range []string{"terminalCause", "\"phase\"", "\"elapsed\""} {
		if strings.Contains(string(b), leak) {
			t.Errorf("progress vocabulary %s leaked into the result payload: %s", leak, b)
		}
	}

	// Without a token, the same operation reports nothing.
	before := len(log.snapshot())
	res, err = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("tokenless check: %v %v", err, res)
	}
	if after := len(log.snapshot()); after != before {
		t.Errorf("tokenless call emitted %d notifications", after-before)
	}
}

// progressPipelineHarness builds a session over the fixture corpus with a
// working verify pipeline (fake backend, injected witness run), capturing
// progress notifications — the harness the gate and context progress
// assertions need.
func progressPipelineHarness(t *testing.T) (*mcp.ClientSession, *notificationLog) {
	t.Helper()
	s := &Server{
		fsys: func() fs.FS {
			return fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte(doc)},
				".stipulator/gaps/m-a.textproto": {Data: []byte("requirement_id: \"REQ-m-a\"\nreason: \"later\"\nlands { manual { condition: \"x\" } }\n")},
				".stipulator/gaps/m-b.textproto": {Data: []byte("requirement_id: \"REQ-m-b\"\nreason: \"later\"\nlands { manual { condition: \"x\" } }\n")},
			}
		},
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{}}, nil
		},
		runTests: func(context.Context) (*verify.TestRun, error) {
			return &verify.TestRun{RaceEnabled: true, Outcomes: map[string]verify.TestOutcome{}}, nil
		},
	}
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(context.Background(), st) }()
	log := &notificationLog{}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			log.add(req.Params)
		},
	})
	sess, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, log
}

// phasesOf decodes the notifications' events and collapses them to the
// distinct phase sequence, returning the terminal cause of the last one.
func phasesOf(t *testing.T, notes []*mcp.ProgressNotificationParams) ([]stipulatorv1.Phase, stipulatorv1.TerminalCause) {
	t.Helper()
	var phases []stipulatorv1.Phase
	cause := stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED
	for _, n := range notes {
		e := &stipulatorv1.ProgressEvent{}
		if err := protojson.Unmarshal([]byte(n.Message), e); err != nil {
			t.Fatalf("notification message is not a ProgressEvent: %v\n%s", err, n.Message)
		}
		if len(phases) == 0 || phases[len(phases)-1] != e.GetPhase() {
			phases = append(phases, e.GetPhase())
		}
		cause = e.GetTerminalCause()
	}
	return phases, cause
}

// TestGateAndContextToolsReportPhasedProgress pins the shared seam on the
// other long-running tools: a gate call with a progress token reports its
// pipeline phases through coverage, and a context call with the expensive
// slice leg reports the context-slice phase — both sealed COMPLETED.
func TestGateAndContextToolsReportPhasedProgress(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	sess, log := progressPipelineHarness(t)
	params := &mcp.CallToolParams{Name: "gate", Arguments: map[string]any{}}
	params.SetProgressToken("gate-progress")
	res, err := sess.CallTool(context.Background(), params)
	if err != nil || res.IsError {
		t.Fatalf("gate: %v %v", err, res)
	}
	notes := waitNotifications(t, log, 5)
	phases, cause := phasesOf(t, notes)
	want := []stipulatorv1.Phase{
		stipulatorv1.Phase_PHASE_COMPILE,
		stipulatorv1.Phase_PHASE_EXECUTION,
		stipulatorv1.Phase_PHASE_VERIFICATION,
		stipulatorv1.Phase_PHASE_COVERAGE,
	}
	if len(phases) != len(want) {
		t.Fatalf("gate phase sequence = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("gate phase sequence = %v, want %v", phases, want)
		}
	}
	if cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED {
		t.Errorf("gate terminal cause = %v, want COMPLETED", cause)
	}

	sess2, log2 := progressPipelineHarness(t)
	params = &mcp.CallToolParams{Name: "context", Arguments: map[string]any{"ids": "REQ-m-a", "slice": true}}
	params.SetProgressToken("context-progress")
	res, err = sess2.CallTool(context.Background(), params)
	if err != nil || res.IsError {
		t.Fatalf("context: %v %v", err, res)
	}
	notes = waitNotifications(t, log2, 6)
	phases, cause = phasesOf(t, notes)
	sliced := false
	for _, p := range phases {
		if p == stipulatorv1.Phase_PHASE_CONTEXT_SLICE {
			sliced = true
		}
	}
	if !sliced {
		t.Errorf("context slice call never reported the context-slice phase: %v", phases)
	}
	if cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED {
		t.Errorf("context terminal cause = %v, want COMPLETED", cause)
	}
}

// TestVerifyPrunePartitionsToolsReportPhasedProgress pins the shared
// seam on the remaining pipeline tools: verify, prune, and partitions
// calls carrying a progress token report the pipeline's phases as
// notifications and seal COMPLETED — the same contract the check, gate,
// and context tools already honor.
func TestVerifyPrunePartitionsToolsReportPhasedProgress(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	pipeline := []stipulatorv1.Phase{
		stipulatorv1.Phase_PHASE_COMPILE,
		stipulatorv1.Phase_PHASE_EXECUTION,
		stipulatorv1.Phase_PHASE_VERIFICATION,
	}
	cases := []struct {
		tool string
		args map[string]any
		min  int
	}{
		{"verify", map[string]any{}, 4},
		{"prune", map[string]any{"check": true}, 5},
		{"partitions", map[string]any{}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			sess, log := progressPipelineHarness(t)
			params := &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args}
			params.SetProgressToken(tc.tool + "-progress")
			res, err := sess.CallTool(context.Background(), params)
			if err != nil || res.IsError {
				t.Fatalf("%s: %v %v", tc.tool, err, res)
			}
			notes := waitNotifications(t, log, tc.min)
			phases, cause := phasesOf(t, notes)
			if len(phases) < len(pipeline) {
				t.Fatalf("%s phase sequence = %v, want the pipeline prefix %v", tc.tool, phases, pipeline)
			}
			for i, p := range pipeline {
				if phases[i] != p {
					t.Fatalf("%s phase sequence = %v, want the pipeline prefix %v", tc.tool, phases, pipeline)
				}
			}
			if cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED {
				t.Errorf("%s terminal cause = %v, want COMPLETED", tc.tool, cause)
			}
		})
	}
}

// TestVerifyToolDeadlineNamesExpiredPhaseAndCause pins deadline
// attribution on the verify handler: a deadline expiring while the
// pipeline executes the policy yields an error naming the execution phase
// and the deadline cause, with the context error preserved for
// programmatic dispatch.
func TestVerifyToolDeadlineNamesExpiredPhaseAndCause(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	s := &Server{
		fsys: func() fs.FS {
			return fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte(doc)},
			}
		},
		backends: func(context.Context) (map[string]verify.Backend, error) {
			return map[string]verify.Backend{"go": fakeBackend{}}, nil
		},
		runTests: func(ctx context.Context) (*verify.TestRun, error) {
			// The policy execution outlasts any deadline.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "verify"}}
	_, _, err := s.toolVerify(ctx, req, verifyIn{})
	if err == nil {
		t.Fatal("deadline-terminated verify returned no error")
	}
	if !strings.Contains(err.Error(), "deadline expired in the execution phase") {
		t.Errorf("error does not name the expired phase and cause: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("context cause lost: %v", err)
	}
}

// TestCheckToolClientCancellationSealsProgress pins the wire leg of
// cancellation: a client cancelling its call cancels the check
// operation's context, and the terminal progress event still reaches the
// session naming the cancellation and the phase it landed in.
func TestCheckToolClientCancellationSealsProgress(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-cancellation", "REQ-mcp-progress")
	started := make(chan struct{})
	stopped := make(chan struct{})
	sess, log := checkHarness(t, func(ctx context.Context, _ bool) (*stipulatorv1.CheckResult, error) {
		progress.FromContext(ctx).Phase(stipulatorv1.Phase_PHASE_EXECUTION)
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		params := &mcp.CallToolParams{Name: "check", Arguments: map[string]any{}}
		params.SetProgressToken("cancel-progress")
		_, err := sess.CallTool(ctx, params)
		done <- err
	}()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("check operation did not receive the client's cancellation")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("tool call error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tool call did not return after cancellation")
	}
	// The terminal event outlives the request context: it names the
	// cancellation and the phase it landed in.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range log.snapshot() {
			e := &stipulatorv1.ProgressEvent{}
			if protojson.Unmarshal([]byte(n.Message), e) == nil &&
				e.GetTerminalCause() == stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED {
				if e.GetPhase() != stipulatorv1.Phase_PHASE_EXECUTION {
					t.Fatalf("terminal event names phase %v, want EXECUTION", e.GetPhase())
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no terminal CANCELLED event reached the session: %v", log.snapshot())
}
