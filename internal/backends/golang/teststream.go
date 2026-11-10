package golang

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// streamState is the parsed form of one package's command stream.
type streamState struct {
	// terminal is the package-level terminal action: "pass", "fail",
	// "skip", or empty when the stream ended without one.
	terminal string
	// failedBuild reports a build-fail event or a terminal fail event
	// naming a failed build.
	failedBuild bool
	events      int
	// malformed retains the first unparseable bytes, when any; the
	// diagnostic renders a bounded share of it (remainderShare) and
	// marks the cut.
	malformed string
	// postTerminal reports events after the terminal package event — a
	// shape the toolchain never produces, refused rather than trusted.
	postTerminal bool
	// sawAbort reports abort output (a panic, a runtime fatal) anywhere in
	// the stream: the testlog flush of such a process cannot be trusted.
	sawAbort bool
	// binaryTimeout records that the test binary's own deadline panic
	// ("panic: test timed out after <dur>") appeared, carrying the
	// panic's printed duration. Detection only: the classifier
	// reclassifies solely under a reviewed declared bound, and the
	// diagnostic names that reviewed bound, never this printed value.
	binaryTimeout string
	// roster collects the deadline panic's own "running tests:" entries
	// — the testing runtime's account of the subjects the deadline cut
	// off. Event ordering cannot identify them: a completed failure's
	// fail event may flush after the panic line, so the runtime's roster
	// is the only trustworthy victim list.
	roster []string
	// rosterOpen tracks being inside the dump's roster block.
	rosterOpen bool
	// pkgOutput is package-level output: build diagnostics and package
	// FAIL/ok lines.
	pkgOutput boundedBuffer
	// perTest accumulates each named test's own output until its terminal
	// event.
	perTest map[string]*boundedBuffer
	// started tracks tests that began and have not reached a terminal
	// event — abort residue when the package dies under them.
	started map[string]bool
	// startOrder preserves first-appearance order for deterministic
	// residue rendering.
	startOrder []string
	// regs accumulates each named test's runtime registrations until its
	// terminal event.
	regs        map[string][]string
	startTimes  map[string]time.Time
	testTimings []*stipulatorv1.TestTimingDiagnostic
	tests       []*stipulatorv1.TestResult
	diags       []*stipulatorv1.FailureDiagnostic
}

// collectTestTimings returns the completed per-test timings plus an entry
// marked running_at_abort for each test that started without reaching a
// terminal event.
func (st *streamState) collectTestTimings(abortedAt time.Time) []*stipulatorv1.TestTimingDiagnostic {
	if st == nil {
		return nil
	}
	out := slices.Clone(st.testTimings)
	for _, name := range startedTests(st) {
		td := &stipulatorv1.TestTimingDiagnostic{}
		td.SetTest(name)
		td.SetOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_NOT_RUN)
		td.SetRunningAtAbort(true)
		if start := st.startTimes[name]; !start.IsZero() && abortedAt.After(start) {
			td.SetElapsed(durationpb.New(abortedAt.Sub(start)))
		} else {
			td.SetElapsed(durationpb.New(0))
		}
		out = append(out, td)
	}
	return out
}

// parseTestStream consumes Go's test/build JSON stream, recording named
// test outcomes attributed to producer — subtests under the same producer
// as their parent, in stream order — with each occurrence's runtime
// registrations, and retaining bounded failure output. It never
// classifies health — classification needs the process exit and context
// state the caller holds.
func parseTestStream(invocation, pkg string, r io.Reader, producer *stipulatorv1.ProducerIdentity) *streamState {
	st := &streamState{
		perTest:    map[string]*boundedBuffer{},
		started:    map[string]bool{},
		regs:       map[string][]string{},
		startTimes: map[string]time.Time{},
	}
	dec := json.NewDecoder(r)
	for {
		var e testEvent
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			// An unparseable line poisons the stream: retain what remains
			// for the diagnostic and stop trusting anything after it.
			var rest boundedBuffer
			rest.write(err.Error())
			rest.write("; unparsed remainder: ")
			buf := make([]byte, failureOutputCap)
			m, _ := io.ReadFull(io.MultiReader(dec.Buffered(), r), buf)
			rest.write(string(buf[:m]))
			// Drain so the child never blocks on a full pipe.
			_, _ = io.Copy(io.Discard, r)
			st.malformed = rest.b.String()
			return st
		}
		st.events++
		if st.terminal != "" {
			// The terminal package event ends a well-formed stream; the
			// classifier refuses anything that follows it.
			st.postTerminal = true
		}
		if e.Action == "output" {
			if isAbortOutput(e.Output) {
				st.sawAbort = true
			}
			st.scanTimeoutDump(e.Output)
		}
		switch e.Action {
		case "build-output":
			st.pkgOutput.write(e.Output)
			continue
		case "build-fail":
			st.failedBuild = true
			continue
		}
		if e.Test == "" {
			switch e.Action {
			case "output":
				st.pkgOutput.write(e.Output)
			case "pass", "fail", "skip":
				st.terminal = e.Action
				if e.FailedBuild != "" {
					st.failedBuild = true
				}
			}
			continue
		}
		switch e.Action {
		case "run":
			if !st.started[e.Test] && st.perTest[e.Test] == nil {
				st.startOrder = append(st.startOrder, e.Test)
			}
			st.started[e.Test] = true
			if !e.Time.IsZero() && st.startTimes[e.Test].IsZero() {
				st.startTimes[e.Test] = e.Time
			}
		case "output":
			bb := st.perTest[e.Test]
			if bb == nil {
				bb = &boundedBuffer{}
				st.perTest[e.Test] = bb
				if !st.started[e.Test] {
					st.startOrder = append(st.startOrder, e.Test)
				}
			}
			bb.write(e.Output)
			// Runtime registrations attribute to the exact test whose
			// output carried them — subtest-granular by construction.
			for _, m := range coversRe.FindAllStringSubmatch(e.Output, -1) {
				st.regs[e.Test] = append(st.regs[e.Test], m[1])
			}
		case "pass", "fail", "skip":
			delete(st.started, e.Test)
			tr := &stipulatorv1.TestResult{}
			tr.SetPackage(pkg)
			tr.SetTest(e.Test)
			tr.SetOutcome(outcomeOf(e.Action))
			tr.SetProducer(producer)
			if regs := st.regs[e.Test]; len(regs) > 0 {
				sort.Strings(regs)
				tr.SetRegistrations(slices.Compact(regs))
				delete(st.regs, e.Test)
			}
			st.tests = append(st.tests, tr)
			td := &stipulatorv1.TestTimingDiagnostic{}
			td.SetTest(e.Test)
			td.SetOutcome(outcomeOf(e.Action))
			dur := time.Duration(e.Elapsed * float64(time.Second))
			if dur == 0 {
				if start := st.startTimes[e.Test]; !start.IsZero() && !e.Time.IsZero() && e.Time.After(start) {
					dur = e.Time.Sub(start)
				}
			}
			td.SetElapsed(durationpb.New(dur))
			st.testTimings = append(st.testTimings, td)
			delete(st.startTimes, e.Test)
			if e.Action == "fail" {
				d := &stipulatorv1.FailureDiagnostic{}
				d.SetInvocation(invocation)
				d.SetPackage(pkg)
				d.SetTest(e.Test)
				d.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
				if bb := st.perTest[e.Test]; bb != nil {
					d.SetOutput(bb.b.String())
					d.SetTruncated(bb.truncated)
				}
				st.diags = append(st.diags, d)
			}
			delete(st.perTest, e.Test)
		}
	}
	return st
}

// scanTimeoutDump feeds one output event's lines to the deadline-panic
// recognizer: the panic line itself, then the dump's "running tests:"
// roster, closed by the dump's goroutine section header. Line-oriented
// so the dump parses identically whether the toolchain delivers it as
// one event or one event per line.
func (st *streamState) scanTimeoutDump(s string) {
	for line := range strings.Lines(s) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case st.binaryTimeout == "":
			if strings.HasPrefix(line, "panic: test timed out") {
				if m := binaryTimeoutRe.FindStringSubmatch(line); m != nil {
					st.binaryTimeout = m[1]
				}
			}
		case line == "\trunning tests:":
			st.rosterOpen = true
		case st.rosterOpen:
			if m := timeoutRosterRe.FindStringSubmatch(line); m != nil {
				st.roster = append(st.roster, m[1])
			} else if strings.HasPrefix(line, "goroutine ") {
				// The dump's goroutine section header ends the roster.
				// Goroutines still running while the dump prints can
				// interleave their own writes — a bare newline included —
				// between entries; skipping everything else instead of
				// closing keeps the roster complete, and nothing after a
				// dump can match the entry shape (frames are single-tab
				// with no space before the parenthesis), so a roster the
				// header never closes collects nothing further either.
				st.rosterOpen = false
			}
		}
	}
}

// runResidue renders what a run left behind, in one order for every
// diagnostic that carries it — the caller's head (a degrade reason, a
// deadline sentence, none), then the unparsed stream remainder when
// the stream was poisoned (the refusal's own evidence, ahead of
// anything the cap could spend on: the classifier refuses a poisoned
// stream before its terminal ladder, so only the degrade and cut-off
// diagnostics carry it), the package-level output, each
// started-but-unfinished test's name with its buffered output where
// it left any (a test with no terminal event died with the package,
// and its output is the failure's residue under its name; on the
// deadline arm the head's roster is what attributes the denied
// subjects — an unfinished test the runtime did not list is sectioned
// for its output alone), the child's stderr (where an
// envelope kill's goroutine dump lands), and the process exit —
// truncation propagated from every buffer rendered. The remainder is
// rendered as a bounded share (remainderShare) with its cut marked: a
// poison is identified by its prefix, and a cap-sized remainder must
// not displace the package output — a poisoned build stream's
// compiler diagnostic — that the rest of the cap retains. The cut-off,
// degrade, and terminal-fail diagnostics are this one composition
// with their own head.
func runResidue(st *streamState, stderr *boundedBuffer, waitErr error, head string) *boundedBuffer {
	var out boundedBuffer
	section := func(s string) {
		if !out.empty() {
			out.write("\n")
		}
		out.write(s)
	}
	if head != "" {
		section(head)
	}
	if st.malformed != "" {
		section("malformed stream: ")
		// Raw stream bytes: scrubbed to valid text, since the
		// diagnostic rides a UTF-8-validated proto field whose marshal
		// refuses the whole report otherwise (the JSON-decoded sections
		// are valid by decoding; this one and stderr are not).
		scrubbed := strings.ToValidUTF8(st.malformed, "\uFFFD")
		remainder := cutAtRune(scrubbed, remainderShare)
		if len(remainder) < len(scrubbed) {
			// The mark compares the cut against the text it cut: the
			// scrub can widen a byte into a three-byte rune, so the raw
			// length is no measure of it.
			out.truncated = true
		}
		out.write(remainder)
	}
	if !st.pkgOutput.empty() {
		section("package output:\n")
		out.write(st.pkgOutput.b.String())
		out.truncated = out.truncated || st.pkgOutput.truncated
	}
	for _, name := range st.startOrder {
		bb := st.perTest[name]
		if !st.started[name] || bb == nil || bb.empty() {
			continue
		}
		section(fmt.Sprintf("--- aborted: %s ---\n", name))
		out.write(bb.b.String())
		out.truncated = out.truncated || bb.truncated
	}
	if !stderr.empty() {
		section("stderr:\n")
		out.write(strings.ToValidUTF8(stderr.b.String(), "�"))
		out.truncated = out.truncated || stderr.truncated
	}
	if waitErr != nil {
		section(fmt.Sprintf("process exit: %v", waitErr))
	}
	return &out
}

// startedTests returns, in first-appearance order, the tests a cut-off
// stream had started without finishing.
func startedTests(st *streamState) []string {
	var names []string
	for _, name := range st.startOrder {
		if st.started[name] {
			names = append(names, name)
		}
	}
	return names
}

func outcomeOf(action string) stipulatorv1.TestOutcome {
	switch action {
	case "pass":
		return stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED
	case "fail":
		return stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED
	}
	return stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED
}

// classifyRun turns a parsed stream plus its process exit into the
// package's terminal disposition. The refusal ladder comes first: a
// malformed stream, a stream without a terminal package/build event, or a
// process that produced no events at all — exit status notwithstanding —
// is degraded, never healthy, because a report that cannot prove the suite
// ran cannot certify it passed.
func classifyRun(invocation, pkg string, st *streamState, waitErr error, stderr *boundedBuffer, binaryBound string) packageRun {
	run := packageRun{pkg: pkg, tests: st.tests, diags: st.diags}
	degrade := func(reason string) packageRun {
		out := runResidue(st, stderr, waitErr, reason)
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED
		d := &stipulatorv1.FailureDiagnostic{}
		d.SetInvocation(invocation)
		d.SetPackage(pkg)
		d.SetDisposition(run.disposition)
		d.SetOutput(out.b.String())
		d.SetTruncated(out.truncated)
		run.diags = append(run.diags, d)
		return run
	}
	// `go test -c -json` can end at build-fail without emitting a test
	// package terminal. That compiler fact plus an ordinary nonzero exit
	// establishes build failure; a missing artifact, failed start, or signal
	// does not. Ownership and conversion faults poison their stream before
	// reaching this shared classifier.
	var exit *exec.ExitError
	failedCompilation := st.failedBuild && errors.As(waitErr, &exit) && exit.ExitCode() > 0
	switch {
	case st.malformed != "":
		return degrade("go test -json stream carried unparseable output")
	case st.events == 0:
		return degrade("go test -json produced no events; a silent command stream is refused")
	case st.terminal == "" && !failedCompilation:
		return degrade("go test -json stream ended without a terminal package event")
	case st.postTerminal:
		return degrade("go test -json stream carried events after the terminal package event; a stream that outlives its own verdict is refused")
	}
	switch st.terminal {
	case "pass", "skip":
		if waitErr != nil {
			// A green stream from a red process is a contradiction the
			// report must not paper over.
			return degrade("go test exited with failure despite a passing stream")
		}
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
		return run
	}
	// Terminal fail: a build failure when the toolchain says so; a binary
	// deadline when the testing runtime's own panic appeared AND the
	// reviewed record declares a binary bound — the reviewed bound is the
	// red fact the diagnostic names (REQ-policy-budget-attribution), and
	// detection alone never reclassifies a run whose record declares no
	// deadline; otherwise suite semantics — assertion failures, panics,
	// red TestMain — exactly the failure classes a direct `go test`
	// exits non-zero for. Completed outcomes, a genuine failure whose
	// fail event flushed after the panic line included, stand untouched
	// in every arm.
	deadline := st.binaryTimeout != "" && binaryBound != "" && !st.failedBuild
	switch {
	case st.failedBuild:
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED
	case deadline:
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
	default:
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
	}
	head := ""
	rosterTruncated := false
	if deadline {
		head = fmt.Sprintf("test binary timeout %s exhausted before the package completed — the budget is the red fact, not the running tests", binaryBound)
		// The runtime's own roster is the victim list; event ordering is
		// not (a completed failure can flush after the panic line). The
		// started set stands in only when the dump carried no roster.
		names := st.roster
		if len(names) == 0 {
			names = startedTests(st)
		}
		if len(names) > 0 {
			var line string
			line, rosterTruncated = formatBoundedRoster("\nrunning when the budget expired: ", names)
			head += line
		}
	}
	out := runResidue(st, stderr, waitErr, head)
	out.truncated = out.truncated || rosterTruncated
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(invocation)
	d.SetPackage(pkg)
	d.SetDisposition(run.disposition)
	d.SetOutput(out.b.String())
	d.SetTruncated(out.truncated)
	run.diags = append(run.diags, d)
	return run
}

// heldByPrefix opens the timeout diagnostic's line naming the memory
// term that held a package until the envelope expired.
const heldByPrefix = "held by the memory term: "

// packageReasonBound bounds the reason's line in a no-outcome cause.
const packageReasonBound = 200

// packageReason is the host's part of a package's no-outcome cause,
// read from the package-scoped diagnostic that carries the package's
// own disposition under the invocation — the degradation's own, which
// the classifier appends after any stream diagnostics: for a degraded
// package (a memory refusal, a toolchain or telemetry degradation, a
// stream the classifier refused) that diagnostic's first line; for a
// timed-out package the memory term's line when the term held it,
// nothing otherwise (a timeout or a test failure is the package's own
// outcome, its cause the disposition alone). The line is bounded;
// empty when the package carries no such diagnostic.
func packageReason(diags []*stipulatorv1.FailureDiagnostic, invocation, pkg string, disposition stipulatorv1.HealthDisposition) string {
	var own *stipulatorv1.FailureDiagnostic
	for _, d := range diags {
		if d.GetInvocation() == invocation && d.GetPackage() == pkg && d.GetTest() == "" && d.GetDisposition() == disposition {
			own = d
		}
	}
	if own == nil {
		return ""
	}
	switch disposition {
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
		line, _, _ := strings.Cut(own.GetOutput(), "\n")
		return boundedReason(line)
	case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
		// The term's line is the diagnostic's second line, where
		// finalizeRun writes it — never read from the cut-off process's
		// residue below, whose text is the test's own.
		if _, rest, ok := strings.Cut(own.GetOutput(), "\n"); ok {
			if line, _, _ := strings.Cut(rest, "\n"); strings.HasPrefix(line, heldByPrefix) {
				return boundedReason(line)
			}
		}
	}
	return ""
}

// boundedReason bounds a no-outcome cause's line. The memory term's
// line — opening with the refusal's or the held line's prefix and the
// term's first words — keeps its deciding part whole — the estimate
// and its origin, everything before the first "; " — and bounds the
// readings that follow (REQ-evidence-admission-origin); any other
// line is bounded whole.
func boundedReason(line string) string {
	if strings.HasPrefix(line, "memory: "+termLead) || strings.HasPrefix(line, heldByPrefix+termLead) {
		if head, rest, ok := strings.Cut(line, "; "); ok {
			return head + "; " + cutAtRune(rest, packageReasonBound)
		}
	}
	return cutAtRune(line, packageReasonBound)
}

// termLead opens the memory term's words: the estimate and its origin
// lead, the readings follow.
const termLead = "one package estimated at "

// degradedRun is a spawn-stage degradation: the package never produced a
// stream at all.
func degradedRun(invocation, pkg, reason string, truncated bool) packageRun {
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(invocation)
	d.SetPackage(pkg)
	d.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	d.SetOutput(reason)
	d.SetTruncated(truncated)
	return packageRun{
		pkg:         pkg,
		disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED,
		diags:       []*stipulatorv1.FailureDiagnostic{d},
	}
}

// writerFunc adapts a function to io.Writer (the bounded stderr sink, the
// engine diagnostics sink).
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
