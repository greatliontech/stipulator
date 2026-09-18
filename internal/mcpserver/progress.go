package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/progress"
)

// The progress reporting and terminal-error composition the served
// verbs share.

// startProgress arms one tool call's progress seam: the returned context
// carries a Reporter whose phase tracking backs terminal-cause
// attribution, and — only when the client asked, by sending a progress
// token — whose bounded events ride MCP progress notifications
// (REQ-mcp-progress). Progress never enters result payloads: the sink is
// the notification channel and nothing else. The sink is non-blocking —
// the transport write happens on NonBlocking's sender goroutine — so a
// stalled progress-consuming client costs dropped advisory events, never
// the operation's cancellability. Notifications are sent on a
// cancellation-free context because the terminal event must still reach
// the client after the request context ends.
func (s *Server) startProgress(ctx context.Context, req *mcp.CallToolRequest) (context.Context, *progress.Reporter) {
	var sink func(*stipulatorv1.ProgressEvent)
	if token := req.Params.GetProgressToken(); token != nil {
		session := req.Session
		notifyCtx := context.WithoutCancel(ctx)
		// NonBlocking's one sender goroutine calls send serially, so the
		// counter needs no lock; MCP requires the progress value to
		// increase with every notification.
		var seq float64
		sink = progress.NonBlocking(func(e *stipulatorv1.ProgressEvent) {
			b, err := protojson.Marshal(e)
			if err != nil {
				return
			}
			seq++
			_ = session.NotifyProgress(notifyCtx, &mcp.ProgressNotificationParams{
				ProgressToken: token,
				Message:       string(b),
				Progress:      seq,
			})
		})
	} else if req != nil && req.Session != nil {
		// No token: progress notifications are unaddressable, so the one
		// remaining token-free channel carries a bounded liveness trace -
		// phase transitions only, as info-level log messages. The SDK
		// sends nothing unless the client has set a log level, so this is
		// free for clients that cannot consume it; a client that set a
		// level distinguishes slow work from a hang without a token
		// (REQ-mcp-progress's liveness bound). The session guard covers
		// direct in-process calls that carry no wire request.
		session := req.Session
		notifyCtx := context.WithoutCancel(ctx)
		var phases progress.PhaseTracker
		sink = progress.NonBlocking(func(e *stipulatorv1.ProgressEvent) {
			// Notes are bounded by the policy (one per executing
			// invocation, one per persisting unit), so they ride the
			// liveness channel beside the phase transitions.
			if note := e.GetNote(); note != "" {
				_ = session.Log(notifyCtx, &mcp.LoggingMessageParams{
					Level:  "info",
					Logger: "stipulator",
					Data:   fmt.Sprintf("%s (%s elapsed)", note, e.GetElapsed().AsDuration().Round(time.Second)),
				})
			}
			if !phases.Changed(e) {
				return
			}
			_ = session.Log(notifyCtx, &mcp.LoggingMessageParams{
				Level:  "info",
				Logger: "stipulator",
				Data:   fmt.Sprintf("phase %s (%s elapsed)", progress.Word(e.GetPhase()), e.GetElapsed().AsDuration().Round(time.Second)),
			})
		})
	}
	prog := progress.New(sink)
	return progress.NewContext(ctx, prog), prog
}

// terminalToolError seals a failed call's progress and names its terminal
// cause. A call that ends at a deadline or a client cancellation
// identifies the phase it died in and which of the two ended it — so a
// client can distinguish long-running work, deadline expiry,
// cancellation, and server failure without guessing (REQ-mcp-progress,
// REQ-mcp-cancellation); any other operational fault is a server
// failure and speaks for itself.
func terminalToolError(prog *progress.Reporter, ctx context.Context, err error) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return fmt.Errorf("%s: %w", prog.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE), err)
	case context.Canceled:
		// The client's cancellation: the line names the cause, the
		// phase, and what the operation kept.
		return fmt.Errorf("%s: %w", prog.SealBy(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED, "the client"), err)
	}
	if errors.Is(err, policy.ErrRecord) {
		// A missing or invalid accepted test policy is a fact about the
		// tree, not a server fault: the unified check fails its verdict on
		// exactly this condition (REQ-check-verdict), so the tool call
		// carries the test-failure cause and names the record's path
		// beside the loader's guidance — an agent must distinguish
		// no-policy from server failure without guessing.
		prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE)
		return fmt.Errorf("%s: %w", policy.Path, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE)
	return err
}
