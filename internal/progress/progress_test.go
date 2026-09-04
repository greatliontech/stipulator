package progress

import (
	"context"
	"strings"
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

// The completed-call timing line: entered phases render in order under
// one total — the notification-blind client's after-the-fact record
// (REQ-mcp-progress). ADJACENT re-entry adds no stamp (the operations'
// phase graphs are linear, which is what bounds the line); a reporter
// that never entered a phase stamps nothing.
func TestStampsRenderAdjacentDedupedPhases(t *testing.T) {
	r := New(nil)
	r.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	r.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	r.Phase(stipulatorv1.Phase_PHASE_VERIFICATION)
	got := r.Stamps()
	if !strings.HasPrefix(got, "took ") {
		t.Fatalf("stamps = %q, want a total-led line", got)
	}
	for _, want := range []string{"compile ", "execution ", "verification "} {
		if !strings.Contains(got, want) {
			t.Fatalf("stamps = %q, missing %q", got, want)
		}
	}
	if strings.Count(got, "compile ") != 1 {
		t.Fatalf("re-entered phase stamped twice: %q", got)
	}
	if ci, ei := strings.Index(got, "compile"), strings.Index(got, "execution"); ci > ei {
		t.Fatalf("stamps out of order: %q", got)
	}
	if fresh := New(nil).Stamps(); fresh != "" {
		t.Fatalf("phaseless reporter stamped %q", fresh)
	}
	var nilReporter *Reporter
	if nilReporter.Stamps() != "" {
		t.Fatal("nil reporter stamped")
	}
}

// TestNotesAndKeptRideTheStream pins the decision lines and the kept
// report (REQ-mcp-progress, REQ-policy-cancellation): a note emits at
// once and exactly once, a persisted unit emits its note and joins the
// kept list, and the terminal event — alone — carries every kept unit.
func TestNotesAndKeptRideTheStream(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress", "REQ-policy-cancellation")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(time.Hour))
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	r.Note("executing race: 3 subjects in 2 packages")
	r.Step("race", 1, 2)
	r.Persisted("race", 3)
	r.Note("")
	r.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED)
	var notes []string
	for _, e := range events {
		if e.GetNote() != "" {
			notes = append(notes, e.GetNote())
		}
		if e.GetTerminalCause() == stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED && len(e.GetKept()) != 0 {
			t.Fatalf("an advisory event carried the kept list: %v", e)
		}
	}
	want := []string{"executing race: 3 subjects in 2 packages", "persisted: race (3 records)"}
	if strings.Join(notes, "|") != strings.Join(want, "|") {
		t.Fatalf("notes = %v, want %v", notes, want)
	}
	final := events[len(events)-1]
	if final.GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED || len(final.GetKept()) != 1 || final.GetKept()[0] != "race (3 records)" {
		t.Fatalf("terminal event = %v, want cancelled with kept race (3 records)", final)
	}
	if got := r.Kept(); len(got) != 1 || got[0] != "race (3 records)" {
		t.Fatalf("Kept() = %v", got)
	}
	r.Note("after the end")
	if events[len(events)-1] != final {
		t.Fatal("a note after the terminal event emitted")
	}
}

// TestStderrSinkRendersEachEventOnce pins the CLI leg of REQ-mcp-progress:
// the stderr sink renders a phase transition once, an invocation's
// progress as completed of total, a note verbatim, and the terminal
// event as its cause with the phase and the kept units — or "kept
// nothing" when a cancelled run persisted none.
func TestStderrSinkRendersEachEventOnce(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var out strings.Builder
	r := New(Stderr(&out), WithInterval(time.Hour))
	r.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	r.Step("race", 2, 2)
	r.Note("executing plain: 1 subjects in 1 packages")
	r.Persisted("plain", 1)
	r.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED)
	got := out.String()
	for _, want := range []string{
		"phase discovery (", "phase execution (", "race: 2/2 packages (",
		"executing plain: 1 subjects in 1 packages (", "persisted: plain (1 records) (",
		"cancelled in the execution phase; kept: plain (1 records)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr rendering lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "phase execution (") != 1 {
		t.Fatalf("phase line repeated:\n%s", got)
	}
	var empty strings.Builder
	e := New(Stderr(&empty), WithInterval(time.Hour))
	e.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	e.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE)
	if !strings.Contains(empty.String(), "deadline expired in the compile phase; kept nothing") {
		t.Fatalf("deadline rendering:\n%s", empty.String())
	}
	if line := TerminalLine(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED, stipulatorv1.Phase_PHASE_VERIFICATION, nil); line != "ended: completed" {
		t.Fatalf("completed line = %q", line)
	}
	// A completed operation ends silently — its pace line is the
	// caller's — and an operation that entered no phase prints nothing
	// at all: no phantom transition, no ending.
	var quiet strings.Builder
	q := New(Stderr(&quiet), WithInterval(time.Hour))
	q.Phase(stipulatorv1.Phase_PHASE_COMPILE)
	q.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	if got := quiet.String(); !strings.HasPrefix(got, "phase compile (") || strings.Count(got, "\n") != 1 {
		t.Fatalf("completed run rendered %q, want the phase line alone", got)
	}
	// The unspecified phase is never entered: marking it changes
	// nothing, stamps nothing, transitions nothing.
	q2 := New(Stderr(&quiet), WithInterval(time.Hour))
	q2.Phase(stipulatorv1.Phase_PHASE_UNSPECIFIED)
	if q2.CurrentPhase() != stipulatorv1.Phase_PHASE_UNSPECIFIED || q2.Stamps() != "" || strings.Count(quiet.String(), "\n") != 1 {
		t.Fatalf("the unspecified phase was entered: %q, stamps %q", quiet.String(), q2.Stamps())
	}
	// Nor is it entered FROM a phase: the reporter stays where it was,
	// with one stamp and one rendered transition.
	var from strings.Builder
	q3 := New(Stderr(&from), WithInterval(time.Hour))
	q3.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	q3.Phase(stipulatorv1.Phase_PHASE_UNSPECIFIED)
	if q3.CurrentPhase() != stipulatorv1.Phase_PHASE_EXECUTION || strings.Count(q3.Stamps(), ",") != 0 || strings.Count(from.String(), "\n") != 1 {
		t.Fatalf("marking the unspecified phase from execution: phase %v, stamps %q, rendered %q", q3.CurrentPhase(), q3.Stamps(), from.String())
	}
	var none strings.Builder
	n := New(Stderr(&none), WithInterval(time.Hour))
	n.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE)
	if none.Len() != 0 {
		t.Fatalf("a run of no phase rendered %q", none.String())
	}
}

// TestNotesAreOneBoundedLine pins the bound on decision lines
// (REQ-mcp-progress): a note quoting a multi-line, multi-kilobyte
// reason reaches the stream as its first line, capped.
func TestNotesAreOneBoundedLine(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(time.Hour))
	r.Note("executing race: 1 subject — 1 re-executed: build failed:\n./x.go:3:1: syntax error\n./y.go:9:2: more")
	r.Note(strings.Repeat("é", 500))
	if len(events) != 2 {
		t.Fatalf("emitted %d events, want 2", len(events))
	}
	if got := events[0].GetNote(); strings.Contains(got, "\n") || !strings.HasSuffix(got, "build failed:") {
		t.Fatalf("multi-line note = %q, want its first line", got)
	}
	if got := []rune(events[1].GetNote()); len(got) != noteBound || got[len(got)-1] != '…' {
		t.Fatalf("long note = %d runes ending %q, want %d ending in an ellipsis", len(got), string(got[len(got)-1]), noteBound)
	}
}

// TestSealRendersAndEmitsAtomically pins the sealed ending: Seal emits
// the terminal event once, returns the same account the event carries,
// and renders again without emitting.
func TestSealRendersAndEmitsAtomically(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress", "REQ-policy-cancellation")
	var events []*stipulatorv1.ProgressEvent
	r := New(collect(&events), WithInterval(time.Hour))
	r.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	r.Persisted("race", 2)
	before := len(events)
	line := r.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED)
	if line != "cancelled in the execution phase; kept: race (2 records)" {
		t.Fatalf("sealed line = %q", line)
	}
	if len(events) != before+1 || events[len(events)-1].GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED || len(events[len(events)-1].GetKept()) != 1 {
		t.Fatalf("seal emitted %v", events[before:])
	}
	if again := r.Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE); again != line || len(events) != before+1 {
		t.Fatalf("second seal rendered %q and emitted %d more", again, len(events)-before-1)
	}
	if got := (&Reporter{}).Seal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED); got == "" {
		t.Fatal("a bare reporter sealed to nothing")
	}
	c := New(nil)
	c.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	if got := c.SealBy(stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED, "the client"); got != "cancelled by the client in the discovery phase; kept nothing" {
		t.Fatalf("sealed by an actor = %q", got)
	}
}
