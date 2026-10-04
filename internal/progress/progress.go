// Package progress is the shared progress seam of the long-running
// operations: one Reporter per operation tracks the current phase and
// per-invocation completion, and emits bounded ProgressEvent
// notifications through a caller-supplied sink. Events are notifications
// only — they never ride result payloads (the report messages carry no
// progress field) and nothing here writes to any output stream, so a
// caller that installs no sink gets phase tracking for terminal-cause
// attribution and emits nothing at all.
//
// Emission is bounded by construction: a phase transition, an
// invocation's completion, and the terminal event always emit
// (milestones), every other event is suppressed inside the reporter's
// minimum interval — so an operation's event count is capped by its
// phase and invocation counts plus its wall time over the interval,
// never by how often the operation reports.
//
// The Reporter rides the context so the seam is additive: every
// operation signature stays unchanged, a nil Reporter (no reporter
// installed) is inert, and deep callees report without threading a
// parameter through orchestration that does not care.
package progress

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/resident"
)

// defaultInterval is the minimum spacing of non-milestone events.
const defaultInterval = time.Second

// noteBound caps a decision line: one line, at most this many runes —
// a reason quoting a multi-line build failure stays one bounded
// notification (REQ-mcp-progress).
const noteBound = 200

// clip bounds text to its first line and noteBound runes.
func clip(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > noteBound {
		text = string(runes[:noteBound-1]) + "…"
	}
	return text
}

// Reporter tracks one operation's progress and emits bounded events.
// All methods are safe for concurrent use and inert on a nil receiver.
type Reporter struct {
	mu               sync.Mutex
	sink             func(*stipulatorv1.ProgressEvent)
	interval         time.Duration
	start            time.Time
	last             time.Time
	phase            stipulatorv1.Phase
	inv              string
	completed, total int32
	// maxDone is the highest completed count recorded per invocation in
	// the current phase: concurrent completion reports race to the lock,
	// so a non-increasing count is stale evidence, suppressed.
	maxDone map[string]int32
	// stamps records each phase transition's entry time, in order - the
	// material of the completed-call timing line (REQ-mcp-progress's
	// notification-blind fallback).
	stamps []phaseStamp
	// kept names, in order, the units whose records persisted: the
	// material of the terminal event's kept list, so a cancelled
	// operation names what it kept (REQ-policy-cancellation).
	kept []string
	// note is the decision line the next emitted event carries; cleared
	// once emitted so no later event repeats it.
	note string
	// cause is the sealed ending: a sealed reporter renders it again
	// whatever a later caller asks, so two renderings never disagree.
	cause stipulatorv1.TerminalCause
	done  bool
	// sample reads the process's resident set; nil reads nothing and no
	// event or digest carries the datum. It is read at every phase
	// transition and at the ending, never per step, so the datum stays
	// as bounded as the transitions (REQ-mcp-progress).
	sample func() (resident.Set, bool)
	// pending is the reading the next emitted event carries; cleared
	// once emitted, like note. The digest's own reading never pends: a
	// keepalive between the digest and the sealing carries nothing.
	pending *stipulatorv1.ResidentSet
	// resident is the digest's material: the readings' running peak
	// against the baseline read at construction, the moment that peak
	// was first reached, the largest descendants reading, and the
	// reading at the end.
	resident residentRecord
}

type phaseStamp struct {
	phase   stipulatorv1.Phase
	entered time.Time
}

// moment names when a reading was taken: the exit of a phase, the
// operation's end, or — the zero value — its start.
type moment struct {
	exitOf stipulatorv1.Phase
	atEnd  bool
}

// residentRecord attributes the readings. The kernel answers a peak as
// the larger of its stored high-water mark and the current set, so two
// readings need not be monotonic: peak is the readings' running
// maximum, and peakMoment the moment it last rose — the moment the
// final peak was first reached. A long-lived process (the server) has
// a peak from before this operation: baseline is its peak at
// construction, and a running peak that never exceeds it is reported
// as reached before the operation, attributed to no moment.
type residentRecord struct {
	taken       bool
	baseline    uint64
	peak        uint64
	peakRaised  bool
	peakMoment  moment
	descendants resident.Set
	descMoment  moment
	descPeak    uint64
	end         *resident.Set
	// largest is the largest resident set among the operation's own
	// readings — stated beside a peak that predates the operation, so a
	// server call whose process peaked earlier still states its own.
	largest uint64
}

// Option configures a Reporter.
type Option func(*Reporter)

// WithInterval sets the minimum spacing of non-milestone events.
func WithInterval(d time.Duration) Option {
	return func(r *Reporter) { r.interval = d }
}

// WithResident installs the resident-set reading the reporter takes at
// every phase transition and at the ending — resident.Sample on a host
// that answers it. Without it no event or digest carries the datum.
func WithResident(sample func() (resident.Set, bool)) Option {
	return func(r *Reporter) { r.sample = sample }
}

// New returns a Reporter emitting through sink. A nil sink still tracks
// the phase — terminal-cause attribution needs it even when the caller
// asked for no notifications — and emits nothing.
func New(sink func(*stipulatorv1.ProgressEvent), opts ...Option) *Reporter {
	r := &Reporter{sink: sink, interval: defaultInterval, start: time.Now()}
	for _, o := range opts {
		o(r)
	}
	if r.sample != nil {
		// The baseline: the process's peak before this operation did
		// anything, so a peak the operation never exceeds is not
		// attributed to one of its phases.
		if set, ok := r.sample(); ok {
			r.resident.baseline, r.resident.peak = set.ProcessPeakBytes, set.ProcessPeakBytes
		}
	}
	return r
}

type ctxKey struct{}

// NewContext returns ctx carrying r.
func NewContext(ctx context.Context, r *Reporter) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, r)
}

// FromContext returns the Reporter ctx carries, or nil — and nil is a
// valid, inert receiver for every method.
func FromContext(ctx context.Context) *Reporter {
	r, _ := ctx.Value(ctxKey{}).(*Reporter)
	return r
}

// Phase records entering p and emits the transition. Re-entering the
// current phase is a no-op, so idempotent marks at nested seams cannot
// inflate the event count. A transition resets the per-invocation state:
// counts belong to the phase that produced them.
func (r *Reporter) Phase(p stipulatorv1.Phase) {
	if r == nil || p == stipulatorv1.Phase_PHASE_UNSPECIFIED {
		// The unspecified phase is the state of having entered none;
		// it is never entered, so no tracker can see a transition into
		// it and no stamp records a nameless phase.
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || r.phase == p {
		return
	}
	r.readResidentLocked(moment{exitOf: r.phase}, true)
	r.phase = p
	r.inv, r.completed, r.total = "", 0, 0
	r.maxDone = nil
	r.stamps = append(r.stamps, phaseStamp{phase: p, entered: time.Now()})
	r.emitLocked(stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED)
}

// Step records per-invocation progress: completed of total work units
// (packages) inside the current phase. The final unit of an invocation
// always emits (a milestone); intermediate steps are rate-limited.
// Reporters of concurrent units race to the lock, so a count can arrive
// after a higher one; the recorded and emitted counts are kept strictly
// increasing per invocation by suppressing the stale arrival — the
// completion milestone fires exactly once, from the max holder.
func (r *Reporter) Step(invocation string, completed, total int32) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	if max, ok := r.maxDone[invocation]; ok && completed <= max {
		return
	}
	if r.maxDone == nil {
		r.maxDone = map[string]int32{}
	}
	r.maxDone[invocation] = completed
	r.inv, r.completed, r.total = invocation, completed, total
	milestone := total > 0 && completed >= total
	if !milestone && time.Since(r.last) < r.interval {
		return
	}
	r.emitLocked(stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED)
}

// Note emits a decision line — what an invocation executes and why its
// subjects serve no record, or what a completed group persisted. A note
// is a milestone, never rate-limited: the callers emit at most one per
// executing invocation and one per persisted group, so the stream stays
// bounded by the policy, never by the test count (REQ-mcp-progress).
func (r *Reporter) Note(text string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || text == "" {
		return
	}
	r.note = clip(text)
	r.emitLocked(stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED)
}

// Persisted records that unit's records installed — records of them —
// and emits the note naming it; the terminal event lists every unit so
// recorded, so a cancelled operation names what it kept.
func (r *Reporter) Persisted(unit string, records int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	entry := fmt.Sprintf("%s (%d records)", clip(unit), records)
	r.kept = append(r.kept, entry)
	r.note = "persisted: " + entry
	r.emitLocked(stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED)
}

// Kept returns the units whose records persisted so far, in order.
func (r *Reporter) Kept() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.kept...)
}

// Keepalive emits the current state, rate-limited, with no state change:
// the bridge for long analysis steps that have phases of their own but no
// countable units at this seam.
func (r *Reporter) Keepalive() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || time.Since(r.last) < r.interval {
		return
	}
	r.emitLocked(stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED)
}

// Terminal emits the final event carrying cause and the phase the
// operation ended in, exactly once; every later call and event is
// dropped, so nothing can report progress after its own verdict. The
// cause must be a concrete terminal cause: an unspecified cause reads as
// an advisory event to sinks that route on it, so the terminal delivery
// guarantee holds only for named causes.
func (r *Reporter) Terminal(cause stipulatorv1.TerminalCause) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.done, r.cause = true, cause
	r.readResidentLocked(moment{atEnd: true}, true)
	r.emitLocked(cause)
}

// Seal ends the operation with cause — the terminal event, exactly
// once — and returns the ending rendered for a person: the cause, the
// phase it ended in, and what it kept, sampled under the same lock the
// event is emitted under, so the message and the event never disagree.
// A sealed reporter renders its ending again without emitting.
func (r *Reporter) Seal(cause stipulatorv1.TerminalCause) string {
	return r.SealBy(cause, "")
}

// SealBy is Seal naming who ended the operation — "the client" for a
// cancellation the MCP client sent — in the rendered line.
func (r *Reporter) SealBy(cause stipulatorv1.TerminalCause, actor string) string {
	if r == nil {
		return terminalLine(cause, stipulatorv1.Phase_PHASE_UNSPECIFIED, nil, actor)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return terminalLine(r.cause, r.phase, r.kept, actor) + r.endingTailLocked()
	}
	r.done, r.cause = true, cause
	r.readResidentLocked(moment{atEnd: true}, true)
	line := terminalLine(cause, r.phase, r.kept, actor) + r.endingTailLocked()
	r.emitLocked(cause)
	return line
}

// endingTailLocked renders the reading at the end as the sealed line's
// tail — the ending in words on every face that prints the line.
func (r *Reporter) endingTailLocked() string {
	if r.resident.end == nil {
		return ""
	}
	return " — " + residentWords(*r.resident.end, moment{atEnd: true})
}

// readResidentLocked takes the resident reading at m — the exit of the
// phase being left at a transition, the end at the ending or at the
// digest rendered before it, the start at the first transition. With
// carry the next emitted event carries it; the digest's reading never
// carries. Every reading advances the attribution.
func (r *Reporter) readResidentLocked(m moment, carry bool) {
	if r.sample == nil {
		return
	}
	set, ok := r.sample()
	if !ok {
		return
	}
	if carry {
		r.pending = residentWire(set, m)
	}
	rec := &r.resident
	// peak starts at the baseline, so the first rise above it is the
	// first reading the operation raised.
	if set.ProcessPeakBytes > rec.peak {
		rec.peak, rec.peakMoment, rec.peakRaised = set.ProcessPeakBytes, m, true
	}
	if !rec.taken || set.DescendantsBytes > rec.descendants.DescendantsBytes {
		rec.descendants, rec.descMoment = set, m
	}
	rec.descPeak = max(rec.descPeak, set.DescendantPeakBytes)
	rec.largest = max(rec.largest, set.ProcessBytes)
	if m.atEnd {
		end := set
		rec.end = &end
	}
	rec.taken = true
}

// residentWire is a reading on the wire with its moment.
func residentWire(set resident.Set, m moment) *stipulatorv1.ResidentSet {
	e := &stipulatorv1.ResidentSet{}
	e.SetProcessBytes(set.ProcessBytes)
	e.SetProcessPeakBytes(set.ProcessPeakBytes)
	e.SetDescendantsBytes(set.DescendantsBytes)
	e.SetDescendants(int32(set.Descendants))
	e.SetDescendantPeakBytes(set.DescendantPeakBytes)
	e.SetExitOf(m.exitOf)
	e.SetAtEnd(m.atEnd)
	return e
}

// CurrentPhase returns the phase the operation is in — the attribution a
// deadline or cancellation error names.
func (r *Reporter) CurrentPhase() stipulatorv1.Phase {
	if r == nil {
		return stipulatorv1.Phase_PHASE_UNSPECIFIED
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.phase
}

func (r *Reporter) emitLocked(cause stipulatorv1.TerminalCause) {
	r.last = time.Now()
	if r.sink == nil {
		return
	}
	e := &stipulatorv1.ProgressEvent{}
	e.SetPhase(r.phase)
	e.SetInvocation(r.inv)
	e.SetElapsed(durationpb.New(time.Since(r.start)))
	e.SetCompleted(r.completed)
	e.SetTotal(r.total)
	e.SetTerminalCause(cause)
	e.SetNote(r.note)
	r.note = ""
	if r.pending != nil {
		e.SetResident(r.pending)
		r.pending = nil
	}
	if cause != stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED {
		e.SetKept(append([]string(nil), r.kept...))
	}
	r.sink(e)
}

// PhaseTracker reports phase transitions once each: the first event of
// a phase changes it, every later event of the same phase does not. Its
// zero value is the unspecified phase, so an event of no phase — an
// operation that entered none — never counts as a transition.
type PhaseTracker struct {
	phase stipulatorv1.Phase
}

// Changed reports whether e enters a phase the tracker has not seen
// last, recording it.
func (p *PhaseTracker) Changed(e *stipulatorv1.ProgressEvent) bool {
	if e.GetPhase() == p.phase {
		return false
	}
	p.phase = e.GetPhase()
	return true
}

// Stderr returns the CLI's sink: each event one line on w — a phase
// transition as the phase and its elapsed time, an invocation's
// progress as completed of total packages, a note verbatim, and an
// interrupted operation's ending as its cause, the phase, and what was
// kept — the same events the MCP surface carries as notifications,
// rendered for a person (REQ-mcp-progress's both-surface leg). A
// completed operation ends silently here: its pace line is the
// caller's, from the stamps. Phase transitions and notes print once
// each; progress lines are already rate-limited by the reporter.
func Stderr(w io.Writer) func(*stipulatorv1.ProgressEvent) {
	var phases PhaseTracker
	return func(e *stipulatorv1.ProgressEvent) {
		elapsed := roundDuration(e.GetElapsed().AsDuration())
		if phases.Changed(e) {
			fmt.Fprintf(w, "phase %s (%s)%s\n", Word(e.GetPhase()), elapsed, ResidentSuffix(e))
		}
		if note := e.GetNote(); note != "" {
			fmt.Fprintf(w, "%s (%s)\n", note, elapsed)
		} else if e.GetTotal() > 0 && e.GetInvocation() != "" {
			fmt.Fprintf(w, "%s: %d/%d packages (%s)\n", e.GetInvocation(), e.GetCompleted(), e.GetTotal(), elapsed)
		}
		if cause := e.GetTerminalCause(); interrupted(cause) {
			fmt.Fprintf(w, "%s%s\n", TerminalLine(cause, e.GetPhase(), e.GetKept()), ResidentSuffix(e))
		}
	}
}

// interrupted reports whether cause ends an operation before its
// verdict — a cancellation or a deadline.
func interrupted(cause stipulatorv1.TerminalCause) bool {
	return cause == stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED || cause == stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE
}

// TerminalLine renders an operation's ending for a person: the cause
// with the phase it ended in, and — when the operation kept anything —
// the units whose records persisted, so a cancelled run names what a
// rerun will serve.
func TerminalLine(cause stipulatorv1.TerminalCause, phase stipulatorv1.Phase, kept []string) string {
	return terminalLine(cause, phase, kept, "")
}

func terminalLine(cause stipulatorv1.TerminalCause, phase stipulatorv1.Phase, kept []string, actor string) string {
	var b strings.Builder
	by := ""
	if actor != "" {
		by = " by " + actor
	}
	switch cause {
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED:
		fmt.Fprintf(&b, "cancelled%s in the %s phase", by, Word(phase))
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE:
		fmt.Fprintf(&b, "deadline expired in the %s phase", Word(phase))
	default:
		fmt.Fprintf(&b, "ended: %s", causeWord(cause))
	}
	if len(kept) > 0 {
		fmt.Fprintf(&b, "; kept: %s", strings.Join(kept, ", "))
	} else if interrupted(cause) {
		b.WriteString("; kept nothing")
	}
	return b.String()
}

// causeWord is the terminal cause's human word.
func causeWord(c stipulatorv1.TerminalCause) string {
	switch c {
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED:
		return "completed"
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_TEST_FAILURE:
		return "test failure"
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_SERVER_FAILURE:
		return "failure"
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_CANCELLED:
		return "cancelled"
	case stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE:
		return "deadline"
	}
	return "unspecified"
}

// sinkBuffer bounds the events a NonBlocking sink holds while its sender
// goroutine drains toward a slow consumer.
const sinkBuffer = 16

// NonBlocking decorates send so the returned sink never blocks the
// reporter. The reporter emits with its mutex held while riding the
// operation, so a stalled consumer must cost events, never the
// operation: events land in a bounded buffer drained by one dedicated
// sender goroutine that calls send serially, and a full buffer drops the
// incoming event — progress is advisory and already rate-limited. The
// terminal event is exempt from dropping: it travels a reserved slot the
// reporter's emit-once seal keeps free, so no flood can cost the one
// event naming the operation's ending, and its delivery ends the sender.
// A permanently stalled consumer strands at most that one goroutine and
// its bounded buffer.
func NonBlocking(send func(*stipulatorv1.ProgressEvent)) func(*stipulatorv1.ProgressEvent) {
	events := make(chan *stipulatorv1.ProgressEvent, sinkBuffer)
	terminal := make(chan *stipulatorv1.ProgressEvent, 1)
	go func() {
		for {
			select {
			case e := <-events:
				send(e)
			case e := <-terminal:
				// Deliver the buffered backlog first so the terminal event
				// stays the last one the consumer sees.
				for {
					select {
					case buffered := <-events:
						send(buffered)
					default:
						send(e)
						return
					}
				}
			}
		}
	}()
	return func(e *stipulatorv1.ProgressEvent) {
		if e.GetTerminalCause() != stipulatorv1.TerminalCause_TERMINAL_CAUSE_UNSPECIFIED {
			select {
			case terminal <- e:
			default:
			}
			return
		}
		select {
		case events <- e:
		default:
		}
	}
}

// Stamps renders the operation's phase timings as one bounded line
// ("took 9.8s: compile 300ms, execution 7.4s, verification 2.1s") - the
// notification-blind client's after-the-fact record that slow work was
// work, not a hang (REQ-mcp-progress). Empty when no phase was entered.
// Boundedness rests on the operations' linear phase graphs: only
// adjacent re-entry dedups, so a phase genuinely revisited would render
// twice - honestly, and every current operation's phases are linear.
func (r *Reporter) Stamps() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stamps) == 0 {
		return ""
	}
	if !r.done && r.resident.end == nil {
		// The digest is the ending's record on the face that renders it
		// before sealing: the reading at the end is taken here, for the
		// attribution alone — no event carries it.
		r.readResidentLocked(moment{atEnd: true}, false)
	}
	now := time.Now()
	var b strings.Builder
	fmt.Fprintf(&b, "took %s: ", roundDuration(now.Sub(r.start)))
	for i, st := range r.stamps {
		end := now
		if i+1 < len(r.stamps) {
			end = r.stamps[i+1].entered
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %s", Word(st.phase), roundDuration(end.Sub(st.entered)))
	}
	if rec := r.resident; rec.taken {
		if rec.end != nil {
			fmt.Fprintf(&b, "; resident at the end %s", ByteWord(rec.end.ProcessBytes))
		}
		if rec.peakRaised {
			fmt.Fprintf(&b, "; peak %s %s", ByteWord(rec.peak), momentWords(rec.peakMoment))
		} else {
			fmt.Fprintf(&b, "; peak %s reached before this operation, this operation's largest reading %s", ByteWord(rec.peak), ByteWord(rec.largest))
		}
		if rec.descendants.Descendants > 0 {
			fmt.Fprintf(&b, "; descendants %s (%d) %s, largest peak %s", ByteWord(rec.descendants.DescendantsBytes), rec.descendants.Descendants, momentWords(rec.descMoment), ByteWord(rec.descPeak))
		}
	}
	return b.String()
}

// EndingLine renders an ending for a face that prints every ending: the
// cause and the phase, with the kept units only when the operation was
// interrupted — a completed operation's units already arrived one per
// persisted group as notes, and the list is unbounded by anything but
// the policy, so repeating it would make the one line grow with the
// policy (REQ-mcp-progress's bound).
func EndingLine(cause stipulatorv1.TerminalCause, phase stipulatorv1.Phase, kept []string) string {
	if !interrupted(cause) {
		kept = nil
	}
	return TerminalLine(cause, phase, kept)
}

// ResidentWords renders an event's resident reading for a person — its
// moment, the process's resident set with its peak, and its live
// descendants' set with their count and the largest one's peak — in the
// words both faces print; empty when the event carries none.
func ResidentWords(e *stipulatorv1.ProgressEvent) string {
	set := e.GetResident()
	if set == nil {
		return ""
	}
	return residentWords(resident.Set{
		ProcessBytes:        set.GetProcessBytes(),
		ProcessPeakBytes:    set.GetProcessPeakBytes(),
		Descendants:         int(set.GetDescendants()),
		DescendantsBytes:    set.GetDescendantsBytes(),
		DescendantPeakBytes: set.GetDescendantPeakBytes(),
	}, moment{exitOf: set.GetExitOf(), atEnd: set.GetAtEnd()})
}

func residentWords(set resident.Set, m moment) string {
	words := fmt.Sprintf("%s: resident %s (peak %s)", momentWords(m), ByteWord(set.ProcessBytes), ByteWord(set.ProcessPeakBytes))
	if set.Descendants > 0 {
		words += fmt.Sprintf(", descendants %s (%d), largest peak %s", ByteWord(set.DescendantsBytes), set.Descendants, ByteWord(set.DescendantPeakBytes))
	}
	return words
}

// momentWords names a reading's moment: "at compile's exit", "at the
// start", "at the end".
func momentWords(m moment) string {
	switch {
	case m.atEnd:
		return "at the end"
	case m.exitOf != stipulatorv1.Phase_PHASE_UNSPECIFIED:
		return "at " + Word(m.exitOf) + "'s exit"
	}
	return "at the start"
}

// ResidentSuffix is ResidentWords as a line's tail — the one spelling
// both faces append to a phase line and to the ending's line.
func ResidentSuffix(e *stipulatorv1.ProgressEvent) string {
	words := ResidentWords(e)
	if words == "" {
		return ""
	}
	return " — " + words
}

// ByteWord renders a byte count in binary units, rounded, with one
// decimal above a gibibyte: "75 MiB", "1.0 GiB" — the convention the
// tree's other byte figures use.
func ByteWord(b uint64) string {
	const kib, mib, gib = 1 << 10, 1 << 20, 1 << 30
	// A value that rounds up to the next unit renders in that unit:
	// 1023.6 MiB is "1.0 GiB", never "1024 MiB".
	switch {
	case b >= gib || math.Round(float64(b)/mib) >= 1024:
		return fmt.Sprintf("%.1f GiB", float64(b)/gib)
	case b >= mib || math.Round(float64(b)/kib) >= 1024:
		return fmt.Sprintf("%.0f MiB", float64(b)/mib)
	case b >= kib:
		return fmt.Sprintf("%.0f KiB", float64(b)/kib)
	}
	return fmt.Sprintf("%d B", b)
}

// roundDuration renders a duration at tenth-of-a-second precision - the
// stamp is orientation, not measurement.
func roundDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(100 * time.Millisecond).String()
}

// Word names a phase for human-facing attribution, e.g. a deadline
// error's "in the execution phase".
func Word(p stipulatorv1.Phase) string {
	switch p {
	case stipulatorv1.Phase_PHASE_COMPILE:
		return "compile"
	case stipulatorv1.Phase_PHASE_DISCOVERY:
		return "discovery"
	case stipulatorv1.Phase_PHASE_EXECUTION:
		return "execution"
	case stipulatorv1.Phase_PHASE_VERIFICATION:
		return "verification"
	case stipulatorv1.Phase_PHASE_COVERAGE:
		return "coverage"
	case stipulatorv1.Phase_PHASE_CONTEXT_SLICE:
		return "context-slice"
	}
	return "startup"
}
