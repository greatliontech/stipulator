package progress

import (
	"context"
	"sync"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func collect(events *[]*stipulatorv1.ProgressEvent) func(*stipulatorv1.ProgressEvent) {
	return func(e *stipulatorv1.ProgressEvent) { *events = append(*events, e) }
}

// TestReporterBoundsEventFlood pins the boundedness contract
// (REQ-mcp-progress): an operation reporting arbitrarily often emits at
// most its milestones plus one rate-limited event per interval — a
// thousand step reports inside one interval collapse to the phase
// transition and the invocation-completion milestone.
func TestReporterBoundsEventFlood(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(time.Hour))
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	for i := int32(1); i <= 1000; i++ {
		r.Step("race", i, 1001)
		r.Keepalive()
	}
	r.Step("race", 1001, 1001)
	if len(events) != 2 {
		t.Fatalf("flood of 2002 reports emitted %d events, want 2 (phase transition + completion milestone)", len(events))
	}
	if got := events[0].GetPhase(); got != stipulatorv1.Phase_PHASE_EXECUTION {
		t.Errorf("first event phase = %v, want EXECUTION", got)
	}
	final := events[1]
	if final.GetInvocation() != "race" || final.GetCompleted() != 1001 || final.GetTotal() != 1001 {
		t.Errorf("completion milestone = %v, want race 1001/1001", final)
	}
	if final.GetElapsed() == nil {
		t.Error("event carries no elapsed time")
	}
}

// TestReporterPhaseTransitionsAlwaysEmit pins the milestone rule: each
// distinct phase emits exactly one transition event however small the
// interval budget, and a repeated mark of the current phase is silent.
func TestReporterPhaseTransitionsAlwaysEmit(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(time.Hour))
	phases := []stipulatorv1.Phase{
		stipulatorv1.Phase_PHASE_COMPILE,
		stipulatorv1.Phase_PHASE_DISCOVERY,
		stipulatorv1.Phase_PHASE_EXECUTION,
		stipulatorv1.Phase_PHASE_VERIFICATION,
		stipulatorv1.Phase_PHASE_COVERAGE,
	}
	for _, p := range phases {
		r.Phase(p)
		r.Phase(p) // idempotent re-mark at a nested seam
	}
	if len(events) != len(phases) {
		t.Fatalf("%d phase transitions emitted %d events", len(phases), len(events))
	}
	for i, p := range phases {
		if events[i].GetPhase() != p {
			t.Errorf("event %d phase = %v, want %v", i, events[i].GetPhase(), p)
		}
		if events[i].GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED {
			t.Errorf("non-terminal event %d carries a terminal cause", i)
		}
	}
}

// TestReporterTerminalEmitsOnceAndSeals pins the terminal contract: the
// final event carries the cause and the phase the operation ended in,
// emits exactly once, and nothing reports progress after it.
func TestReporterTerminalEmitsOnceAndSeals(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(0))
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	r.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE)
	r.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	r.Phase(stipulatorv1.Phase_PHASE_COVERAGE)
	r.Step("race", 1, 1)
	r.Keepalive()
	if len(events) != 2 {
		t.Fatalf("emitted %d events, want phase + one terminal", len(events))
	}
	final := events[1]
	if final.GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE {
		t.Errorf("terminal cause = %v, want DEADLINE", final.GetTerminalCause())
	}
	if final.GetPhase() != stipulatorv1.Phase_PHASE_EXECUTION {
		t.Errorf("terminal event names phase %v, want the phase the deadline expired in", final.GetPhase())
	}
	if r.CurrentPhase() != stipulatorv1.Phase_PHASE_EXECUTION {
		t.Errorf("phase moved after terminal: %v", r.CurrentPhase())
	}
}

// TestReporterStepKeepsCompletedCountsIncreasing pins the monotonicity
// guard: concurrent completion reports race to the reporter's lock, so a
// lower count can arrive after a higher one — the stale arrival is
// suppressed, emitted counts are strictly increasing per invocation, and
// the completion milestone fires exactly once, from the max holder.
func TestReporterStepKeepsCompletedCountsIncreasing(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(0))
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	r.Step("race", 1, 3)
	r.Step("race", 3, 3) // the milestone arrives before count 2
	r.Step("race", 2, 3) // stale: suppressed
	r.Step("race", 3, 3) // duplicate milestone: suppressed
	if len(events) != 3 {
		t.Fatalf("emitted %d events, want phase + counts 1 and 3", len(events))
	}
	if events[1].GetCompleted() != 1 || events[2].GetCompleted() != 3 {
		t.Errorf("emitted counts %d, %d, want 1, 3", events[1].GetCompleted(), events[2].GetCompleted())
	}

	// The same property under real interleaving: whatever order the lock
	// grants, the emitted sequence stays strictly increasing and the
	// milestone fires once.
	var raced []*stipulatorv1.ProgressEvent
	rc := New(collect(&raced), WithInterval(0))
	rc.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	const total = 32
	var wg sync.WaitGroup
	for i := int32(1); i <= total; i++ {
		wg.Add(1)
		go func(n int32) {
			defer wg.Done()
			rc.Step("race", n, total)
		}(i)
	}
	wg.Wait()
	prev := int32(-1)
	milestones := 0
	for _, e := range raced[1:] { // events[0] is the phase transition
		if e.GetCompleted() <= prev {
			t.Fatalf("emitted counts not strictly increasing: %d after %d", e.GetCompleted(), prev)
		}
		prev = e.GetCompleted()
		if e.GetCompleted() >= e.GetTotal() {
			milestones++
		}
	}
	if milestones != 1 {
		t.Errorf("completion milestone fired %d times, want exactly once", milestones)
	}
}

// TestNonBlockingSinkShieldsOperationFromStalledConsumer pins the
// non-blocking contract: a consumer that never returns must not block the
// reporter's Phase/Step/Terminal calls — the operation completes, excess
// events are dropped, and the terminal event is still delivered last once
// the consumer drains.
func TestNonBlockingSinkShieldsOperationFromStalledConsumer(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	release := make(chan struct{})
	var mu sync.Mutex
	var delivered []*stipulatorv1.ProgressEvent
	send := func(e *stipulatorv1.ProgressEvent) {
		<-release // stalled until the test releases the consumer
		mu.Lock()
		delivered = append(delivered, e)
		mu.Unlock()
	}
	r := New(NonBlocking(send), WithInterval(0))
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
		// Far beyond the sink's buffer: every call must return without
		// waiting on the stalled consumer.
		for i := int32(1); i <= 4*sinkBuffer; i++ {
			r.Step("race", i, 4*sinkBuffer)
		}
		r.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled consumer blocked the operation's progress calls")
	}
	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		var last *stipulatorv1.ProgressEvent
		if len(delivered) > 0 {
			last = delivered[len(delivered)-1]
		}
		n := len(delivered)
		mu.Unlock()
		if last != nil && last.GetTerminalCause() == stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED {
			// The flood exceeded the buffer, so events were dropped rather
			// than delivered — bounded delivery is the contract.
			if n > sinkBuffer+2 {
				t.Errorf("delivered %d events from a stalled consumer, want at most buffer+in-flight+terminal", n)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("terminal event never reached the consumer")
}

// TestNilReporterAndSinkAreInert pins the seam's additive contract: no
// reporter in the context, and a reporter without a sink, both track
// state without emitting or panicking — the CLI path installs neither a
// reporter nor a sink and must be observably unchanged.
func TestNilReporterAndSinkAreInert(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var r *Reporter
	r.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	r.Step("race", 1, 2)
	r.Keepalive()
	r.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	if got := r.CurrentPhase(); got != stipulatorv1.Phase_PHASE_UNSPECIFIED {
		t.Errorf("nil reporter phase = %v", got)
	}
	if FromContext(context.Background()) != nil {
		t.Error("bare context carries a reporter")
	}

	sinkless := New(nil, WithInterval(0))
	sinkless.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	sinkless.Step("race", 1, 1)
	if got := sinkless.CurrentPhase(); got != stipulatorv1.Phase_PHASE_EXECUTION {
		t.Errorf("sinkless reporter lost phase tracking: %v", got)
	}

	ctx := NewContext(context.Background(), sinkless)
	if FromContext(ctx) != sinkless {
		t.Error("context round trip lost the reporter")
	}
}
