package golang

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/greatliontech/stipulator/internal/resident"
)

// packageEstimateFloor is the least memory the admission assumes one
// package process needs before any process of the invocation has shown
// its peak — a test binary's build and link step legitimately reach it
// (gomutant's oracle ceiling takes the same floor).
const packageEstimateFloor = uint64(1) << 30

// readingsHook is the admission's reading of the host and of the pass —
// resident.Readings in production; a test injects a host that cannot
// hold a process. reapedPeakHook observes each reap the gate receives —
// every process a package's slot spawned, the isolation re-runs
// included — with whether the gate had the process registered and the
// completed peak it folded: a test seam pinning the wiring of the
// completed-package evidence and of the tree attribution.
var (
	readingsHook   = resident.Readings
	reapedPeakHook func(pkg string, pid int, registered bool, peakBytes uint64)
)

// admission gates the spawn of package processes under the derived
// concurrency bound and, where the host reports its memory, the memory
// term: a package process is admitted while fewer than the bound run and
// the host's available memory, less what the running packages are
// estimated still to take, covers one more package at the invocation's
// estimate with the pass's own room to grow back to its peak left
// over. The measure is the host's (internal/resident's readings carry it); the
// family's soft ceilings are collection targets, not needs — the pass's
// need beyond what it holds is its growth, below — so no half of the
// room is set aside for a pass that holds a fraction of it. Both terms
// read this admission's own observations, never the kernel's lifetime
// marks: the pass's peak is the largest set this invocation's
// admission has read at its asks (discovery's peak, over before the
// admission existed, would reserve memory nothing running takes), and
// the estimate is the largest a package's process tree has been seen
// to need — a completed package's largest process from its wait
// status and the largest a registered package's tree has shown in the
// readings — floored at packageEstimateFloor, so it grows as the
// packages' processes do and never shrinks within the invocation, and
// a descendant outside every registered tree (a driver of the pass's own)
// prices no package; each
// running package is reserved its estimate less what ITS tree already
// shows in the process table (its held pages are out of the room
// already, its file-backed pages the page cache's, reclaimable and
// counted available; a tree attributed to the process the executor
// spawned for it; a tree not yet in the table reserves the whole
// estimate), so a burst of asks between the kernel's readings is
// bounded by the term and not only by the processor bound, and one
// tree's overshoot never pays for a sibling's reservation. Every
// process a package's slot spawns — its whole-package process and each
// isolation re-run — is registered at its spawn and reaped with its
// peak the moment its wait returns, so a running package's reservation
// reads whichever of its processes is live and a reaped process's peak
// prices the next spawn at once; between a reap and the slot's next
// spawn the package holds no registered tree and reserves the whole
// estimate — brief, stated. The estimate names its origin wherever the
// term's words appear — the floor, the package whose completed
// process's peak it is, or the package whose live tree showed it — so
// a genuine need and a transient read apart in the refusal, the held
// package's timeout diagnostic and the witnesses' bounded cause
// (REQ-evidence-admission-origin).
// A waiting package re-asks at every completion of the process (the
// readings move) and gives up with the invocation's context, carrying
// the words of the term that held it; a package asked while nothing of
// the process runs and the host cannot hold one process is refused —
// the refusal's words name the readings — rather than waiting on a
// completion that cannot come or spawning into the host's guard; the
// concurrent operations of one process are judged together on the one
// host gate (hostGate), each running package at its own invocation's
// estimate. The term only narrows:
// the processor bound and the inner width the witness environment
// delivers are never widened by it.
type admission struct {
	ctx context.Context
	// gate is the process's one host gate: the mutex and condition
	// every admission of the process shares, and the membership the
	// room is judged over.
	gate    *hostGate
	bound   int
	running int
	// pids are the processes spawned for the running packages — the
	// roots of the trees the reservation attributes.
	pids map[int]bool
	// peak is the largest completed-package peak seen so far;
	// peakOrigin names the package and process that showed it.
	peak       uint64
	peakOrigin estimateOrigin
	// pkgOf names the package each registered process was spawned for.
	pkgOf map[int]string
	// passPeak is the largest resident set the pass has shown in this
	// admission's readings — execution's own peak, the growth term's
	// reference.
	passPeak uint64
	// treePeak is the largest observed live-tree reading per registered
	// process. Compilation and execution roots occupy the slot sequentially;
	// a reaped root's live readings are replaced by its completed peak.
	treePeak map[int]uint64
}

// estimateOrigin names where the estimate's bytes come from: the
// floor, a package's completed process (its reaped peak), or a
// package's live tree (its largest reading under this admission).
type estimateOrigin struct {
	term string
	pkg  string
	pid  int
}

const (
	originFloor     = "the floor"
	originCompleted = "completed process"
	originLiveTree  = "live tree"
)

// words renders the origin for the term's words.
func (o estimateOrigin) words() string {
	switch o.term {
	case originCompleted:
		return fmt.Sprintf("package %s's completed process %d's peak", o.pkg, o.pid)
	case originLiveTree:
		return fmt.Sprintf("package %s's live tree (process %d) in this invocation's readings", o.pkg, o.pid)
	default:
		return originFloor
	}
}

// hostGate is the process's one host gate: the one mutex and condition
// every admission of the process shares, and the set of admissions
// alive — concurrent operations' (a long-lived server's calls, each
// with its own capture, envelope and bound). Every judgment runs
// under the gate's lock over one reading and every member's running
// packages, so the room is never read from a published snapshot (a
// figure that could go stale between an operation's judgments), a
// waiter waits under the lock that guards what it judged (no wakeup
// is lost), every reap, release and end of any member wakes every
// waiter, and an admission captures the gate at its construction
// (REQ-evidence-admission-origin).
type hostGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	members map[*admission]bool
	// gen counts the wakes (every broadcast); reading is the one
	// reading the waiters woken by the latest wake judge over, taken
	// by the first of them — a fresh ask takes its own.
	gen        uint64
	readingGen uint64
	reading    resident.Reading
	readingOK  bool
}

func newHostGate() *hostGate {
	g := &hostGate{members: map[*admission]bool{}}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// wake broadcasts the gate's condition, opening a new wake generation:
// the woken waiters judge over one reading, taken once.
func (g *hostGate) wake() {
	g.gen++
	g.cond.Broadcast()
}

// readingFor answers the reading a judgment runs over, under g.mu: a
// fresh ask takes its own (the host moves without events); a waiter
// woken by the latest wake takes the generation's, read once for the
// whole burst, so a burst of W waiters costs one walk, not W.
func (g *hostGate) readingFor(fresh bool) (resident.Reading, bool) {
	if fresh || g.readingGen != g.gen {
		g.reading, g.readingOK = readingsHook()
		g.readingGen = g.gen
	}
	return g.reading, g.readingOK
}

// theHostGate is the process's gate; a unit pin swaps in a fresh one
// before its admissions are minted.
var theHostGate = newHostGate()

// join adds a under g.mu.
func (g *hostGate) join(a *admission) {
	g.mu.Lock()
	g.members[a] = true
	g.mu.Unlock()
}

// leave removes a — its invocation returned, nothing of it running —
// and wakes every waiter: the room its packages held is free.
func (g *hostGate) leave(a *admission) {
	g.mu.Lock()
	delete(g.members, a)
	g.wake()
	g.mu.Unlock()
}

// member reports whether a is alive on the gate.
func (g *hostGate) member(a *admission) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.members[a]
}

// admissionObserverForTest, when set, sees every admission minted —
// the seam a pin follows an invocation's gate through to its drop;
// nil in production.
var admissionObserverForTest func(*admission)

func newAdmission(ctx context.Context, bound int) *admission {
	a := &admission{ctx: ctx, gate: theHostGate, bound: bound, pids: map[int]bool{}, pkgOf: map[int]string{}, treePeak: map[int]uint64{}}
	if admissionObserverForTest != nil {
		admissionObserverForTest(a)
	}
	a.gate.join(a)
	// The context's end wakes every waiter, which then returns
	// unadmitted. Membership ends at the invocation's return alone: a
	// context ending on a timeout or a cancellation leaves the
	// invocation's packages running through their kill grace, and
	// their trees must be judged until they are reaped and released.
	context.AfterFunc(ctx, func() {
		a.gate.mu.Lock()
		a.gate.wake()
		a.gate.mu.Unlock()
	})
	return a
}

// estimate is the memory one more package process tree is assumed to
// need, given the reading's trees of the registered package processes
// — the observed maxima advance under the gate's lock with the reading
// — and the
// origin of that figure.
func (a *admission) estimate(reading resident.Reading) (uint64, estimateOrigin) {
	need, origin := packageEstimateFloor, estimateOrigin{term: originFloor}
	if a.peak > need {
		need, origin = a.peak, a.peakOrigin
	}
	for _, pid := range slices.Sorted(maps.Keys(a.pids)) {
		if shown := reading.Trees[pid]; shown > a.treePeak[pid] {
			a.treePeak[pid] = shown
		}
		if a.treePeak[pid] > need {
			need, origin = a.treePeak[pid], estimateOrigin{term: originLiveTree, pkg: a.pkgOf[pid], pid: pid}
		}
	}
	return need, origin
}

// room judges the memory term under the gate's lock: whether the host
// can hold one
// more package process beside the pass and the packages already
// running, and the readings' words when it cannot. A host or a pass
// without a reading has no memory term.
func (a *admission) room(fresh bool) (ok bool, words string, othersRunning bool) {
	reading, ok := a.gate.readingFor(fresh)
	if !ok {
		return true, "", false
	}
	set := reading.Set
	if set.ProcessBytes > a.passPeak {
		a.passPeak = set.ProcessBytes
	}
	need, origin := a.estimate(reading)
	// Each running package's tree is reserved the estimate less what it
	// already shows in the process table (its held pages are out of the
	// room already, its file-backed pages reclaimable); a package
	// admitted but not yet registered, or registered but not yet in the
	// table, reserves the whole estimate. Per tree, never netted across
	// trees: a grown sibling's bytes pay for nothing but itself.
	reserved := a.reservation(reading, need)
	// The other admissions of this process — concurrent operations'
	// — hold their running packages against the same available memory,
	// each at its own estimate over this same reading.
	var others, othersShown uint64
	for b := range a.gate.members {
		if b == a {
			continue
		}
		if b.running > 0 {
			othersRunning = true
		}
		needB, _ := b.estimate(reading)
		others += b.reservation(reading, needB)
		for pid := range b.pids {
			othersShown += reading.Trees[pid]
		}
	}
	// The pass's own room to grow back to the largest set any
	// admission of the process has read: the process grows once, and a
	// later operation's admission must not admit into the headroom an
	// earlier one's pass needs.
	passPeak := a.passPeak
	for b := range a.gate.members {
		passPeak = max(passPeak, b.passPeak)
	}
	growth := passPeak - set.ProcessBytes
	// The room is what the host has available: the pass's and the
	// children's held pages are out of it already, and the family's
	// soft ceilings are targets, not needs.
	available := reading.Host.AvailableBytes
	if available >= others+reserved+need && available-others-reserved-need >= growth {
		return true, "", othersRunning
	}
	// The estimate and its origin lead the words: the witnesses' cause
	// carries the line bounded (packageReasonBound), and the deciding
	// part must survive the cut — the readings follow.
	words = fmt.Sprintf(termLead+"%s — %s; the host cannot hold one more package process beside the pass: available %s, %d package(s) running reserving %s, the pass's resident %s (this phase's peak %s)",
		resident.ByteWord(need), origin.words(), resident.ByteWord(available), a.running, resident.ByteWord(reserved), resident.ByteWord(set.ProcessBytes), resident.ByteWord(passPeak))
	if othersRunning {
		words += fmt.Sprintf(", this process's other operations running %d package(s) reserving %s beyond the %s their trees show", a.gate.runningOthers(a), resident.ByteWord(others), resident.ByteWord(othersShown))
	}
	return false, words, othersRunning
}

// reservation is what the admission's running packages still reserve
// beyond what the reading's table shows of their trees, at need.
func (a *admission) reservation(reading resident.Reading, need uint64) uint64 {
	registered := 0
	reserved := uint64(0)
	for pid := range a.pids {
		registered++
		if shown := reading.Trees[pid]; shown < need {
			reserved += need - shown
		}
	}
	if a.running > registered {
		reserved += uint64(a.running-registered) * need
	}
	return reserved
}

// runningOthers counts the other members' running packages, under g.mu.
func (g *hostGate) runningOthers(a *admission) int {
	n := 0
	for b := range g.members {
		if b != a {
			n += b.running
		}
	}
	return n
}

// admit blocks until the package may spawn. admitted is false when the
// invocation's context ended — held then carries the words of the memory
// term that was holding the package, empty when it waited on the
// processor bound alone — or when nothing of the process runs and the
// host cannot hold one process (refusal names the readings).
func (a *admission) admit() (admitted bool, refusal, held string) {
	g := a.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	fresh := true
	for {
		if a.ctx.Err() != nil {
			return false, "", held
		}
		held = ""
		if a.running < a.bound {
			ok, words, othersRunning := a.room(fresh)
			if ok {
				a.running++
				return true, "", ""
			}
			// Nothing of this invocation running and no other operation
			// of the process running a package: no completion can come,
			// so the package is refused rather than held; another
			// operation's package completes in time, and its reap,
			// release or end wakes this waiter.
			if a.running == 0 && !othersRunning {
				return false, words, ""
			}
			held = words
		}
		g.cond.Wait()
		fresh = false
	}
}

// spawned registers a process an admitted package's executor spawned
// for pkg — the whole-package process or an isolation re-run — the
// root of the tree the reservation attributes to the package.
func (a *admission) spawned(pkg string, pid int) {
	if a == nil {
		return
	}
	a.gate.mu.Lock()
	a.pids[pid] = true
	a.pkgOf[pid] = pkg
	a.gate.mu.Unlock()
}

// childProcessPeakBytes derives a reaped child process's post-exec peak from
// its wait-status maxrss, the parent process's high-water mark, and the live
// process-tree peak observed while the child was registered. On Linux, Go's
// process-group spawn clones with CLONE_VM | CLONE_VFORK and exec_mmap seeds
// the child's signal->maxrss with get_mm_hiwater_rss(old_mm), clamping the
// child's wait-status Maxrss from below by the parent's pre-exec high-water
// mark (RUSAGE_SELF.Maxrss). When parentPeak exceeds packageEstimateFloor and
// rawPeak does not exceed parentPeak, rawPeak reflects the inherited parent
// high-water ceiling rather than a post-exec child peak above the floor, so
// the child's post-exec evidence is bounded by its observed live-tree peak and
// the sub-floor wait-status mark; any rawPeak strictly above parentPeak is
// genuine post-exec child or descendant memory.
func childProcessPeakBytes(rawPeak, parentPeak, liveTreePeak uint64) uint64 {
	if parentPeak > packageEstimateFloor && rawPeak <= parentPeak {
		return max(liveTreePeak, min(rawPeak, packageEstimateFloor))
	}
	return max(rawPeak, liveTreePeak)
}

// reapedState unregisters a package's reaped OS process and folds its
// post-exec completed peak into the estimate after discounting any pre-exec
// parent high-water mark inherited across CLONE_VM | CLONE_VFORK.
func (a *admission) reapedState(pkg string, pid int, state *os.ProcessState) {
	if a == nil {
		return
	}
	rawPeak := processPeakBytes(state)
	parentPeak := parentPeakBytes()
	a.gate.mu.Lock()
	liveTreePeak := a.treePeak[pid]
	a.gate.mu.Unlock()
	a.reaped(pkg, pid, childProcessPeakBytes(rawPeak, parentPeak, liveTreePeak))
}

// reaped unregisters a package's process the moment its wait returned
// and folds its completed peak into the estimate, naming the origin,
// then wakes every waiter of the process to re-ask (the readings
// move); the seam sees what the gate received. The package's slot
// stays held until release.
func (a *admission) reaped(pkg string, pid int, peakBytes uint64) {
	if a == nil {
		return
	}
	a.gate.mu.Lock()
	registered := a.pids[pid]
	delete(a.pids, pid)
	delete(a.treePeak, pid)
	delete(a.pkgOf, pid)
	if peakBytes > a.peak {
		a.peak, a.peakOrigin = peakBytes, estimateOrigin{term: originCompleted, pkg: pkg, pid: pid}
	}
	a.gate.wake()
	a.gate.mu.Unlock()
	if reapedPeakHook != nil {
		reapedPeakHook(pkg, pid, registered, peakBytes)
	}
}

// release returns an admitted package's slot — its processes already
// reaped — and wakes every waiter of the process to re-ask: a slot
// freed is room any operation may take.
func (a *admission) release() {
	a.gate.mu.Lock()
	a.running--
	a.gate.wake()
	a.gate.mu.Unlock()
}

// leave ends the admission's membership of the gate: its invocation
// returned.
func (a *admission) leave() { a.gate.leave(a) }

func (a *admission) originWords() string {
	if a == nil {
		return ""
	}
	a.gate.mu.Lock()
	defer a.gate.mu.Unlock()
	if a.peak > packageEstimateFloor {
		return a.peakOrigin.words()
	}
	return originFloor
}

// witnessSpawnBound shares the parent's processor budget over the permitted
// package population. Each package is a parallel process tree; reserving slots
// for packages that cannot execute would unnecessarily narrow every child.
func witnessSpawnBound(processors, packages int) int {
	return min(max(1, processors/2), max(1, packages))
}

// spawnBoundOf is every post-normalize consumer's road to the fan-out
// bound: the pre-capture freeze (n.SpawnBound), so the spawn and
// every later report of it observe one value — a re-derivation at use
// time would race dynamic GOMAXPROCS updates exactly as the WitnessEnv
// field doc describes for the inner width. The fallback re-derivation
// exists only for hand-built invocations in tests.
func spawnBoundOf(n *NormalizedInvocation) int {
	if n.SpawnBound > 0 {
		return n.SpawnBound
	}
	return max(1, processorBudgetOf(n)/2)
}

// witnessChildWidth derives one unit's inner-parallelism width: the
// parent's processor budget over the unit bound, floored at one, so
// units x per-unit width stays at most the processor count - without
// it each unit is a full-width process tree and the fan-out multiplies
// into units x procs runnable threads, the load the unit bound alone
// never limited. Deriving from the parent's own GOMAXPROCS budget
// (not the raw core count) honors an operator who already narrowed
// the stipulator process.
func witnessChildWidth(n *NormalizedInvocation) int {
	width := processorBudgetOf(n) / spawnBoundOf(n)
	if width < 1 {
		width = 1
	}
	return width
}
