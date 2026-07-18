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
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// defaultInterval is the minimum spacing of non-milestone events.
const defaultInterval = time.Second

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
	done    bool
}

// Option configures a Reporter.
type Option func(*Reporter)

// WithInterval sets the minimum spacing of non-milestone events.
func WithInterval(d time.Duration) Option {
	return func(r *Reporter) { r.interval = d }
}

// New returns a Reporter emitting through sink. A nil sink still tracks
// the phase — terminal-cause attribution needs it even when the caller
// asked for no notifications — and emits nothing.
func New(sink func(*stipulatorv1.ProgressEvent), opts ...Option) *Reporter {
	r := &Reporter{sink: sink, interval: defaultInterval, start: time.Now()}
	for _, o := range opts {
		o(r)
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
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || r.phase == p {
		return
	}
	r.phase = p
	r.inv, r.completed, r.total = "", 0, 0
	r.maxDone = nil
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
	r.done = true
	r.emitLocked(cause)
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
	r.sink(e)
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
