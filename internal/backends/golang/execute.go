package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/progress"
)

// The policy executor runs each normalized Go invocation exactly once and
// derives its terminal health from the `go test -json` streams of its
// selected packages, one owned child process per package. Per-package
// processes are what make attribution honest: every outcome in the report
// names the one process whose stream produced it (REQ-policy-attribution),
// and per-process observation refines the same boundary. The
// executor trusts nothing silent: a stream that ends without a terminal
// package event, carries unparseable bytes, or disagrees with its process
// exit status is degraded, never healthy — an environment that swallowed a
// suite must be distinguishable from a suite that passed.

// failureOutputCap bounds the retained output of one failure diagnostic.
// Retention is part of the verdict; the cap keeps a pathological stream
// (a runaway goroutine dump, a looping test) from turning the report into
// the log it summarizes. Truncation is always marked, never silent.
const failureOutputCap = 64 << 10

// isAbortOutput recognizes the output of a dying test binary. A test that
// legitimately prints these words costs a spurious untrusted-stream
// classification for its process — its evidence is refused, nothing more.
func isAbortOutput(s string) bool {
	return strings.Contains(s, "panic: ") || strings.Contains(s, "fatal error: ")
}

// boundedBuffer retains at most failureOutputCap bytes and records that it
// dropped the rest.
type boundedBuffer struct {
	b         strings.Builder
	truncated bool
}

func (bb *boundedBuffer) write(s string) {
	room := failureOutputCap - bb.b.Len()
	if room <= 0 {
		bb.truncated = bb.truncated || s != ""
		return
	}
	if len(s) > room {
		s = s[:room]
		bb.truncated = true
	}
	bb.b.WriteString(s)
}

func (bb *boundedBuffer) empty() bool { return bb.b.Len() == 0 && !bb.truncated }

// testEvent is the subset of test2json (and go build -json) output the
// executor reads. Build events carry ImportPath and no Package; test
// events carry Package. FailedBuild on a terminal fail event names the
// package whose compilation failed.
type testEvent struct {
	Action      string
	Package     string
	ImportPath  string
	Test        string
	Output      string
	FailedBuild string
}

// packageRun is one selected package's parsed execution. A zero
// disposition means the run reached no terminal fact of its own — the
// caller classifies it as timeout or discards it on cancellation.
// aborted carries the names of tests that had started but not finished
// when the process ended — whether the envelope cut it off or the
// package died under them with a terminal verdict — and residue the
// bounded output a cut-off process left behind. producer is set exactly
// when a process was launched; obs is that process's owned observation,
// absent until the caller classifies a cut-off run.
type packageRun struct {
	pkg         string
	disposition stipulatorv1.HealthDisposition
	aborted     []string
	residue     *boundedBuffer
	producer    *stipulatorv1.ProducerIdentity
	obs         *ProcessObservation
	tests       []*stipulatorv1.TestResult
	diags       []*stipulatorv1.FailureDiagnostic
}

// ExecuteInvocation executes one normalized invocation's selected packages
// — the package obligations of selection — each in its own owned,
// cancellable `go test -json` process, fanned out under the invocation's
// reviewed concurrency bound with the invocation's reviewed
// envelope timeout governing the whole invocation as a context deadline.
// Every selected package executes whole: the exported executor accepts
// no test selection, so the health-judged path is structurally unable to
// narrow — health is a property of the entire declared invocation
// (REQ-core-one-execution), and witness-only narrowing lives behind
// ExecuteSelection, which never grants health.
// It returns the invocation's terminal health — carrying the resolved
// pin-at-load configuration as its evidentiary record — with every
// selected package disposed, the named test outcomes attributed to their
// producing process, bounded failure diagnostics, and one owned
// observation per launched process. Caller cancellation discards the
// partial run: the return is (nil, nil, nil, nil, ctx.Err()), never a
// partial report (REQ-policy-cancellation). Envelope expiry is not
// cancellation — it is a terminal fact, reported as TIMEOUT dispositions
// with each cut-off launched process owning an incomplete observation.
func ExecuteInvocation(ctx context.Context, n *NormalizedInvocation, selection []Obligation) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation, error) {
	pkgs := selectedPackages(selection)
	if len(pkgs) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("invocation %q: selection carries no package obligations", n.Name)
	}
	// The envelope carries its identity as the context cause: the kill path
	// dumps and graces only on true envelope expiry, never on a caller's
	// own deadline — a caller-bounded run is discarded whole, so a dump
	// there would have no consumer and the grace would only delay the
	// abort.
	invCtx, cancel := context.WithTimeoutCause(ctx, n.Timeout, errEnvelopeExpired)
	defer cancel()
	runs := runSelectedPackages(ctx, invCtx, n, pkgs, nil, spawnOrdinals())
	if err := ctx.Err(); err != nil {
		// Caller cancellation: the partial run is discarded whole. The
		// envelope context is derived from ctx, so every child is already
		// terminated through its owned process boundary.
		return nil, nil, nil, nil, err
	}
	return assembleInvocation(n, runs, invCtx.Err() != nil)
}

// spawnOrdinals issues process spawn ordinals, unique within one
// execution, so pid reuse never aliases two launched processes.
func spawnOrdinals() func() int32 {
	var (
		mu   sync.Mutex
		next int32
	)
	return func() int32 {
		mu.Lock()
		defer mu.Unlock()
		next++
		return next
	}
}

// runSelectedPackages fans the packages out under the invocation's
// reviewed concurrency bound, one owned process per package narrowed to
// its tests selection, with invCtx — the invocation envelope — governing
// every spawn. Runs the envelope denied before their spawn come back
// with no terminal disposition for the caller to classify.
func runSelectedPackages(ctx, invCtx context.Context, n *NormalizedInvocation, pkgs []string, tests TestSelection, spawnOrdinal func() int32) []packageRun {
	bound := witnessSpawnBound(n)
	sem := make(chan struct{}, bound)
	runs := make([]packageRun, len(pkgs))
	rep := progress.FromContext(ctx)
	var pkgsDone atomic.Int32
	var wg sync.WaitGroup
	for i, pkg := range pkgs {
		wg.Add(1)
		go func(i int, pkg string) {
			defer wg.Done()
			// Every package reports its completion exactly once, whichever
			// way it ends; the reporter bounds emission.
			defer func() { rep.Step(n.Name, pkgsDone.Add(1), int32(len(pkgs))) }()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-invCtx.Done():
				// Never spawned: the caller classifies the missing terminal
				// disposition as timeout or discards on cancellation.
				runs[i] = packageRun{pkg: pkg}
				return
			}
			runs[i] = runPackage(invCtx, n, pkg, tests[pkg], spawnOrdinal())
		}(i, pkg)
	}
	wg.Wait()
	return runs
}

// finalizeRun classifies a run that reached no terminal fact of its own:
// under envelope expiry it becomes a reported TIMEOUT disposition — the
// cut-off process's retained output is part of the verdict
// (REQ-check-diagnostics), never discarded with the run, and a launched
// process that died before its testlog flushed still owns its
// observation, incomplete rather than silently absent. Outside expiry a
// missing terminal disposition has no in-spec cause and is surfaced as an
// error. soloTest names the single isolated runnable a solo process ran,
// so a denied re-run's timeout diagnostic names the test it denied.
func finalizeRun(n *NormalizedInvocation, r *packageRun, timedOut bool, soloTest string) error {
	if r.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED {
		return nil
	}
	// No terminal disposition of its own: the envelope deadline is the
	// only in-spec way to get here without caller cancellation.
	if !timedOut {
		return fmt.Errorf("invocation %q: package %s ended without a terminal disposition outside timeout and cancellation", n.Name, r.pkg)
	}
	r.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(n.Name)
	d.SetPackage(r.pkg)
	if soloTest != "" {
		d.SetTest(soloTest)
	}
	d.SetDisposition(r.disposition)
	var out boundedBuffer
	out.write(fmt.Sprintf("invocation timeout %v expired before the package completed", n.Timeout))
	if len(r.aborted) > 0 {
		out.write("\nstarted but unfinished: ")
		out.write(strings.Join(r.aborted, ", "))
	}
	if r.residue != nil && !r.residue.empty() {
		out.write("\n")
		out.write(r.residue.b.String())
		out.truncated = out.truncated || r.residue.truncated
	}
	d.SetOutput(out.b.String())
	d.SetTruncated(out.truncated)
	r.diags = append(r.diags, d)
	if r.producer != nil {
		r.obs = incompleteObservation(r.pkg, r.producer,
			fmt.Sprintf("invocation timeout %v expired before the process completed", n.Timeout))
	}
	return nil
}

// assembleInvocation turns the terminal runs into the invocation report:
// per-package health, attributed outcomes, diagnostics, and observations,
// with the invocation disposed as its worst package.
func assembleInvocation(n *NormalizedInvocation, runs []packageRun, timedOut bool) (*stipulatorv1.InvocationHealth, []*stipulatorv1.TestResult, []*stipulatorv1.FailureDiagnostic, []*ProcessObservation, error) {
	health := &stipulatorv1.InvocationHealth{}
	health.SetInvocation(n.Name)
	health.SetGo(resolvedConfig(n))
	packages := make([]*stipulatorv1.PackageHealth, 0, len(runs))
	var (
		tests        []*stipulatorv1.TestResult
		diags        []*stipulatorv1.FailureDiagnostic
		observations []*ProcessObservation
	)
	invDisposition := stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
	for i := range runs {
		r := &runs[i]
		if err := finalizeRun(n, r, timedOut, ""); err != nil {
			return nil, nil, nil, nil, err
		}
		ph := &stipulatorv1.PackageHealth{}
		ph.SetPackage(r.pkg)
		ph.SetDisposition(r.disposition)
		packages = append(packages, ph)
		tests = append(tests, r.tests...)
		diags = append(diags, r.diags...)
		if r.obs != nil {
			observations = append(observations, r.obs)
		}
		invDisposition = worseDisposition(invDisposition, r.disposition)
	}
	health.SetDisposition(invDisposition)
	health.SetPackages(packages)
	return health, tests, diags, observations, nil
}

// worseDisposition aggregates package dispositions into the invocation's:
// healthy only when every package is, otherwise the most report-shaping
// failure wins — timeout over degradation over build failure over test
// failure — so the invocation names the reason its report cannot be
// trusted further.
func worseDisposition(a, b stipulatorv1.HealthDisposition) stipulatorv1.HealthDisposition {
	rank := func(d stipulatorv1.HealthDisposition) int {
		switch d {
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT:
			return 4
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED:
			return 3
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED:
			return 2
		case stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// selectedPackages extracts the sorted package obligations of a selection.
func selectedPackages(selection []Obligation) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, o := range selection {
		if o.Kind == ObligationPackage && !seen[o.Package] {
			seen[o.Package] = true
			pkgs = append(pkgs, o.Package)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// witnessSpawnBound derives the package fan-out bound: the invocation's
// reviewed witness_concurrency when set, else max(1, GOMAXPROCS/2) —
// each unit is itself a parallel process tree, so a full
// processor-count fan-out multiplies into host-freezing load that
// nice(1)'s CPU priority does not cover.
func witnessSpawnBound(n *NormalizedInvocation) int {
	if n.WitnessConcurrency > 0 {
		return int(n.WitnessConcurrency)
	}
	bound := runtime.GOMAXPROCS(0) / 2
	if bound < 1 {
		bound = 1
	}
	return bound
}

// runPackage executes one package's `go test -json` in an owned child
// process — narrowed to the selected top-level runnables when selection
// is non-nil, the test binary's testlog directed to a per-process capture
// file the executor owns — and classifies its stream. A cancelled or
// deadline-expired context leaves the disposition unspecified: the caller
// — not the stream parser — decides between timeout reporting and
// cancellation discard.
func runPackage(ctx context.Context, n *NormalizedInvocation, pkg string, selection []string, ordinal int32) packageRun {
	// Directing the test binary's testlog to a per-process capture file
	// makes the run uncacheable to the toolchain (extra binary arguments
	// fall outside its cacheable set): observation capture deliberately
	// trades toolchain cache hits for per-process evidence — a cached
	// replay has no process, so nothing could own its observation — and
	// witness freshness serving is the sanctioned cache
	// (REQ-evidence-witness-freshness).
	// A failed capture-file creation never blocks execution: the run
	// proceeds without a testlog and the process's observation is
	// incomplete for that stated reason.
	logPath := ""
	if logf, err := os.CreateTemp("", "stipulator-testlog-*.txt"); err == nil {
		logPath = logf.Name()
		logf.Close()
		defer os.Remove(logPath)
	}
	// The observation bracket is captured strictly before the process
	// spawns: it must fingerprint the declared roots as they were when the
	// run could first read them, so a change under a declared root
	// persisting across the run-to-ingest span moves it. A capture failure
	// never blocks execution — the process's observation is incomplete for
	// the stated reason, exactly as a failed capture-file creation.
	frame := captureObservationFrame(ctx, n, pkg)
	cmd := commandContext(ctx, "go", testCommandArgs(n, pkg, selection, logPath)...)
	cmd.Dir = n.Dir
	cmd.Env = witnessProcessEnv(n, frame)
	var stderr boundedBuffer
	cmd.Stderr = writerFunc(func(p []byte) (int, error) {
		stderr.write(string(p))
		return len(p), nil
	})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if ctx.Err() != nil {
			return packageRun{pkg: pkg}
		}
		return degradedRun(n.Name, pkg, fmt.Sprintf("spawning go test: %v", err), false)
	}
	if err := cmd.Start(); err != nil {
		// A spawn refused by an expired or cancelled context is not an
		// environmental degradation: the caller classifies the missing
		// terminal fact as timeout or discards the run.
		if ctx.Err() != nil {
			return packageRun{pkg: pkg}
		}
		return degradedRun(n.Name, pkg, fmt.Sprintf("spawning go test: %v", err), false)
	}
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation(n.Name)
	producer.SetProcessId(int64(cmd.Process.Pid))
	producer.SetProcessOrdinal(ordinal)

	st := parseTestStream(n.Name, pkg, stdout, producer)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		// Keep the parsed residue: on envelope expiry the caller's
		// timeout diagnostic names the tests the cutoff aborted, carries
		// the bounded output the cut-off process left behind — the
		// kill-time goroutine dump arrives on the child's stdout and
		// stderr — and the launched process gains its incomplete
		// observation.
		return packageRun{pkg: pkg, aborted: startedTests(st), residue: cutoffResidue(st, &stderr), producer: producer}
	}
	run := classifyRun(n.Name, pkg, st, waitErr, &stderr)
	// A terminal run retains its started-but-unfinished tests: a package
	// abort's shadowed tests are structural facts the selective isolation
	// pass consumes, not only diagnostic prose.
	run.aborted = startedTests(st)
	run.producer = producer
	run.obs = observeProcess(n, pkg, producer, st, waitErr, run.disposition, logPath, frame)
	return run
}

// testCommandArgs renders one package's `go test -json` argument list from
// the normalized invocation: the typed configuration and nothing ambient,
// plus the per-process testlog capture file when one exists and the
// top-level test selection when one is given — both executor property no
// reviewed args entry may name (validation refuses the collisions, so the
// capture is always the executor's own file and the selection always the
// executor's own rendering, bound to exactly this process).
//
// A non-nil selection renders as an anchored, alternation-of-literals
// `-run` flag on the go command — never a binary argument, so the
// toolchain applies it to every test binary it builds for the package.
// Anchoring is per top-level runnable: subtests and committed fuzz seeds
// ride their selected parent (a single-element Fuzz selection replays the
// target's committed seeds, exactly the ordinary run's replay obligation).
//
// The toolchain's implicit per-binary timeout is disabled outright: the
// reviewed record is the only source of test bounds. The envelope timeout
// governs the whole invocation through owned process termination, and a
// finer per-binary bound rides the reviewed args — the test binary honors
// the last -test.timeout it parses. Left in force, the implicit default
// would abort a reviewed long-running invocation at ten minutes: the go
// command derives both the binary's default -test.timeout and its own
// SIGQUIT kill backstop from its -timeout flag, and binary-level
// arguments cannot reach that backstop, so the inherited ceiling must be
// disabled at the go level, never overridden per binary.
func testCommandArgs(n *NormalizedInvocation, pkg string, selection []string, logPath string) []string {
	args := []string{"test", "-json", "-timeout=0"}
	if n.Race {
		args = append(args, "-race")
	}
	if len(n.Tags) > 0 {
		args = append(args, "-tags="+strings.Join(n.Tags, ","))
	}
	if flag := moduleModeFlag(n.ModuleMode); flag != "" {
		args = append(args, flag)
	}
	if n.PGO != "" {
		pgo := n.PGO
		if pgo != "auto" && pgo != "off" {
			// The committed value is tree-relative; the child runs in the
			// module root, so resolve against the tree root.
			pgo = filepath.Join(treeRoot(n), filepath.FromSlash(pgo))
		}
		args = append(args, "-pgo="+pgo)
	}
	switch {
	case n.CacheBypass:
		args = append(args, "-count=1")
	case n.Count > 0:
		args = append(args, fmt.Sprintf("-count=%d", n.Count))
	}
	if len(selection) > 0 {
		quoted := make([]string, len(selection))
		for i, name := range selection {
			quoted[i] = regexp.QuoteMeta(name)
		}
		args = append(args, "-run=^("+strings.Join(quoted, "|")+")$")
	}
	args = append(args, pkg)
	if logPath != "" || len(n.Args) > 0 {
		args = append(args, "-args")
		if logPath != "" {
			args = append(args, "-test.testlogfile="+logPath)
		}
		args = append(args, n.Args...)
	}
	return args
}

// treeRoot recovers the verification tree root from the normalized
// invocation's absolute module directory and tree-relative module root.
func treeRoot(n *NormalizedInvocation) string {
	if n.ModuleRoot == "" {
		return n.Dir
	}
	return strings.TrimSuffix(n.Dir, string(filepath.Separator)+filepath.FromSlash(n.ModuleRoot))
}

// streamState is the parsed form of one package's command stream.
type streamState struct {
	// terminal is the package-level terminal action: "pass", "fail",
	// "skip", or empty when the stream ended without one.
	terminal string
	// failedBuild reports a build-fail event or a terminal fail event
	// naming a failed build.
	failedBuild bool
	events      int
	// malformed retains the first unparseable bytes, when any.
	malformed string
	// postTerminal reports events after the terminal package event — a
	// shape the toolchain never produces, refused rather than trusted.
	postTerminal bool
	// sawAbort reports abort output (a panic, a runtime fatal) anywhere in
	// the stream: the testlog flush of such a process cannot be trusted.
	sawAbort bool
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
	regs  map[string][]string
	tests []*stipulatorv1.TestResult
	diags []*stipulatorv1.FailureDiagnostic
}

// parseTestStream consumes one `go test -json` stream, recording named
// test outcomes attributed to producer — subtests under the same producer
// as their parent, in stream order — with each occurrence's runtime
// registrations, and retaining bounded failure output. It never
// classifies health — classification needs the process exit and context
// state the caller holds.
func parseTestStream(invocation, pkg string, r io.Reader, producer *stipulatorv1.ProducerIdentity) *streamState {
	st := &streamState{
		perTest: map[string]*boundedBuffer{},
		started: map[string]bool{},
		regs:    map[string][]string{},
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
		if e.Action == "output" && isAbortOutput(e.Output) {
			st.sawAbort = true
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

// cutoffResidue renders the bounded output a cut-off run leaves behind —
// package-level output, each aborted test's buffered output, any unparsed
// stream remainder, and the child's stderr, where the envelope kill's
// goroutine dump lands — for the caller's timeout diagnostic.
func cutoffResidue(st *streamState, stderr *boundedBuffer) *boundedBuffer {
	var out boundedBuffer
	if !st.pkgOutput.empty() {
		out.write("package output:\n")
		out.write(st.pkgOutput.b.String())
		out.truncated = out.truncated || st.pkgOutput.truncated
	}
	for _, name := range st.startOrder {
		if !st.started[name] {
			continue
		}
		bb := st.perTest[name]
		if bb == nil || bb.empty() {
			continue
		}
		out.write(fmt.Sprintf("\n--- aborted: %s ---\n", name))
		out.write(bb.b.String())
		out.truncated = out.truncated || bb.truncated
	}
	if st.malformed != "" {
		out.write("\nmalformed stream: ")
		out.write(st.malformed)
	}
	if !stderr.empty() {
		out.write("\nstderr:\n")
		out.write(stderr.b.String())
		out.truncated = out.truncated || stderr.truncated
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
// malformed stream, a stream without a terminal package event, or a
// process that produced no events at all — exit status notwithstanding —
// is degraded, never healthy, because a report that cannot prove the suite
// ran cannot certify it passed.
func classifyRun(invocation, pkg string, st *streamState, waitErr error, stderr *boundedBuffer) packageRun {
	run := packageRun{pkg: pkg, tests: st.tests, diags: st.diags}
	degrade := func(reason string) packageRun {
		var out boundedBuffer
		out.write(reason)
		if !st.pkgOutput.empty() {
			out.write("\npackage output:\n")
			out.write(st.pkgOutput.b.String())
		}
		if !stderr.empty() {
			out.write("\nstderr:\n")
			out.write(stderr.b.String())
		}
		if st.malformed != "" {
			out.write("\nmalformed stream: ")
			out.write(st.malformed)
		}
		if waitErr != nil {
			out.write(fmt.Sprintf("\nprocess exit: %v", waitErr))
		}
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED
		d := &stipulatorv1.FailureDiagnostic{}
		d.SetInvocation(invocation)
		d.SetPackage(pkg)
		d.SetDisposition(run.disposition)
		d.SetOutput(out.b.String())
		d.SetTruncated(out.truncated || st.pkgOutput.truncated || stderr.truncated)
		run.diags = append(run.diags, d)
		return run
	}
	switch {
	case st.malformed != "":
		return degrade("go test -json stream carried unparseable output")
	case st.events == 0:
		return degrade("go test -json produced no events; a silent command stream is refused")
	case st.terminal == "":
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
	// Terminal fail: a build failure when the toolchain says so, otherwise
	// suite semantics — assertion failures, panics, red TestMain, a
	// go-test-level timeout — exactly the failure classes a direct
	// `go test` exits non-zero for.
	if st.failedBuild {
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED
	} else {
		run.disposition = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
	}
	var out boundedBuffer
	out.write(st.pkgOutput.b.String())
	truncated := st.pkgOutput.truncated
	for _, name := range st.startOrder {
		if !st.started[name] {
			continue
		}
		// A started test with no terminal event died with the package;
		// its buffered output is the failure's residue (a timeout panic,
		// an abort) and belongs to the package diagnostic.
		out.write(fmt.Sprintf("\n--- aborted: %s ---\n", name))
		if bb := st.perTest[name]; bb != nil {
			out.write(bb.b.String())
			truncated = truncated || bb.truncated
		}
	}
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation(invocation)
	d.SetPackage(pkg)
	d.SetDisposition(run.disposition)
	d.SetOutput(out.b.String())
	d.SetTruncated(out.truncated || truncated)
	run.diags = append(run.diags, d)
	return run
}

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

// writerFunc adapts a function to io.Writer for the bounded stderr sink.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// ExecutePolicy executes every Go invocation of the accepted policy
// exactly once against the tree at dir and assembles the execution
// report: per-invocation and per-package terminal health carrying each
// invocation's resolved configuration, attributed test outcomes, bounded
// failure diagnostics, per-process runtime observations, and the
// conservation findings of the policy against the workspace's
// default-selection obligation universe (REQ-policy-conservation). The
// returned observations are the report's own, with each completed
// record's live gofresh evidence beside its wire form for in-process
// consumers — gofresh's producer-side attach path takes the sealed value,
// which has no wire decode. Caller cancellation anywhere — discovery
// included — discards the whole partial report and returns only the
// cancellation error (REQ-policy-cancellation). Invocations execute
// sequentially in record order; concurrency lives inside each invocation,
// bounded per package — so each invocation's envelope bounds only its own
// span and the policy's wall time is the sum of what its invocations
// spend, bounded overall only by the caller's context.
func ExecutePolicy(ctx context.Context, dir string, p *stipulatorv1.TestPolicy) (*stipulatorv1.ExecutionReport, []*ProcessObservation, error) {
	rep := progress.FromContext(ctx)
	rep.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	universe, err := discoverUniverse(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	discovered, err := discoverInvocations(ctx, dir, p)
	if err != nil {
		return nil, nil, err
	}
	rep.Phase(stipulatorv1.Phase_PHASE_EXECUTION)
	var (
		selections   []InvocationSelection
		invocations  []*stipulatorv1.InvocationHealth
		tests        []*stipulatorv1.TestResult
		diags        []*stipulatorv1.FailureDiagnostic
		observations []*ProcessObservation
	)
	for _, d := range discovered {
		selections = append(selections, d.selection)
		health, invTests, invDiags, invObs, err := ExecuteInvocation(ctx, d.normalized, d.selection.Obligations)
		if err != nil {
			return nil, nil, err
		}
		invocations = append(invocations, health)
		tests = append(tests, invTests...)
		diags = append(diags, invDiags...)
		observations = append(observations, invObs...)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	report := &stipulatorv1.ExecutionReport{}
	report.SetInvocations(invocations)
	report.SetTests(tests)
	report.SetObligations(PartitionReports(universe, selections))
	report.SetDiagnostics(diags)
	wire := make([]*stipulatorv1.Observation, len(observations))
	for i, o := range observations {
		wire[i] = o.Wire
	}
	report.SetObservations(wire)
	return report, observations, nil
}
