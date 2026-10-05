package golang

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/resident"
	"github.com/greatliontech/stipulator/stipulate"
)

// injectReadings installs synthetic host and pass readings for the
// admission for the test's life.
func injectReadings(t *testing.T, host func() (resident.Memory, bool), sample func() (resident.Set, bool)) {
	t.Helper()
	prior := readingsHook
	readingsHook = func() (resident.Reading, bool) {
		set, ok := sample()
		if !ok {
			return resident.Reading{}, false
		}
		m, ok := host()
		if !ok {
			return resident.Reading{}, false
		}
		return resident.Reading{Set: set, Host: m}, true
	}
	t.Cleanup(func() { readingsHook = prior })
}

// injectReading installs one synthetic reading, trees included.
func injectReading(t *testing.T, read func() resident.Reading) {
	t.Helper()
	prior := readingsHook
	readingsHook = func() (resident.Reading, bool) { return read(), true }
	t.Cleanup(func() { readingsHook = prior })
}

func hostWith(available uint64) func() (resident.Memory, bool) {
	return func() (resident.Memory, bool) {
		return resident.Memory{TotalBytes: 2 * available, AvailableBytes: available}, true
	}
}

func passWith(peak uint64) func() (resident.Set, bool) {
	return func() (resident.Set, bool) {
		return resident.Set{ProcessBytes: peak / 2, ProcessPeakBytes: peak}, true
	}
}

// admitAsync asks the gate from a goroutine and reports its answer.
func admitAsync(a *admission) <-chan [2]string {
	out := make(chan [2]string, 1)
	go func() {
		admitted, refusal, held := a.admit()
		var verdict string
		switch {
		case admitted:
			verdict = "admitted"
		case refusal == "":
			verdict = "ended"
		default:
			verdict = "refused"
		}
		out <- [2]string{verdict, refusal + held}
	}()
	return out
}

// TestAdmissionGateDerivesTheMemoryTerm pins the admission over
// synthetic readings (REQ-evidence-witness-freshness's witness
// concurrency clause): abundant memory admits exactly the processor
// bound and a further package waits for a release; a host without a
// reading has no memory term; memory that holds one package admits one
// and, nothing else running, refuses the next with the readings named;
// a completed package's peak raises the estimate the refusal states; the
// invocation's end releases a waiter unadmitted and unrefused.
//
//gofresh:pure
func TestAdmissionGateDerivesTheMemoryTerm(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	const gib = uint64(1) << 30
	wait := func(t *testing.T, c <-chan [2]string, want string) [2]string {
		t.Helper()
		select {
		case got := <-c:
			if got[0] != want {
				t.Fatalf("admission = %v, want %s", got, want)
			}
			return got
		case <-time.After(5 * time.Second):
			t.Fatalf("admission did not answer %s", want)
		}
		return [2]string{}
	}
	mustWait := func(t *testing.T, c <-chan [2]string) {
		t.Helper()
		select {
		case got := <-c:
			t.Fatalf("admission answered %v, want it to wait", got)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Abundant memory: the processor bound alone governs.
	injectReadings(t, hostWith(64*gib), passWith(gib))
	a := newAdmission(context.Background(), 3)
	for i := 0; i < 3; i++ {
		if admitted, refusal, _ := a.admit(); !admitted || refusal != "" {
			t.Fatalf("package %d under abundant memory: admitted=%v refusal=%q, want admitted", i, admitted, refusal)
		}
	}
	fourth := admitAsync(a)
	mustWait(t, fourth)
	a.release("", 0, 0)
	wait(t, fourth, "admitted")

	// No host reading, or no pass reading: no memory term, the bound alone.
	injectReadings(t, func() (resident.Memory, bool) { return resident.Memory{}, false }, passWith(100*gib))
	b := newAdmission(context.Background(), 2)
	for i := 0; i < 2; i++ {
		if admitted, _, _ := b.admit(); !admitted {
			t.Fatalf("package %d without a host reading was not admitted", i)
		}
	}
	injectReadings(t, hostWith(gib/4), func() (resident.Set, bool) { return resident.Set{}, false })
	b2 := newAdmission(context.Background(), 2)
	for i := 0; i < 2; i++ {
		if admitted, _, _ := b2.admit(); !admitted {
			t.Fatalf("package %d without a pass reading was not admitted", i)
		}
	}

	// The start burst: the running packages' trees, not yet in the
	// process table, are reserved their estimate — with 3 GiB available
	// and the 1 GiB floor, two are admitted on the same reading and the
	// third waits, whatever the processor bound.
	injectReadings(t, hostWith(3*gib), passWith(gib/4))
	burst := newAdmission(context.Background(), 16)
	for i := 0; i < 2; i++ {
		if admitted, refusal, _ := burst.admit(); !admitted {
			t.Fatalf("package %d of the burst was not admitted: %q", i, refusal)
		}
	}
	third := admitAsync(burst)
	mustWait(t, third)
	burst.release("", 0, 0)
	wait(t, third, "admitted")

	// The reservation is per tree, never netted across trees: four
	// running packages, one tree grown to 8 GiB of small processes and
	// three just-admitted siblings showing nothing, 3 GiB available —
	// the estimate is the trees' share (2 GiB), the three bare siblings
	// reserve 6 GiB, and a fifth package waits; netting the grown
	// tree's bytes against its siblings would have admitted it.
	// The four are admitted while the host is roomy and their trees
	// bare; then the reading moves to the skewed shape.
	current := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 64 * gib},
	}
	var currentMu sync.Mutex
	injectReading(t, func() resident.Reading {
		currentMu.Lock()
		defer currentMu.Unlock()
		return current
	})
	trees := newAdmission(context.Background(), 8)
	for i := 0; i < 4; i++ {
		if admitted, refusal, _ := trees.admit(); !admitted {
			t.Fatalf("running package %d was not admitted: %q", i, refusal)
		}
	}
	for _, pid := range []int{11, 12, 13, 14} {
		trees.spawned(pid)
	}
	currentMu.Lock()
	current = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8, Descendants: 16, DescendantsBytes: 8 * gib, DescendantPeakBytes: gib / 2},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 3 * gib},
		Trees: map[int]uint64{11: 8 * gib},
	}
	currentMu.Unlock()
	fifth := admitAsync(trees)
	mustWait(t, fifth)
	// The three bare siblings gone, the grown tree alone running: its
	// own 8 GiB is now the trees' share, so the estimate is 8 GiB and
	// 3 GiB still holds nothing more — the fifth keeps waiting.
	trees.release("", 12, 0)
	trees.release("", 13, 0)
	trees.release("", 14, 0)
	mustWait(t, fifth)
	// The grown tree completes: nothing runs, the share term is inert,
	// the estimate falls to the floor and the live peak, and 3 GiB
	// holds one — admitted.
	currentMu.Lock()
	current.Set.Descendants, current.Set.DescendantsBytes, current.Trees = 0, 0, nil
	currentMu.Unlock()
	trees.release("", 11, 0)
	wait(t, fifth, "admitted")

	// Memory for exactly one package beside the pass: the first admits;
	// the host's reading drops as it runs (the kernel's available memory
	// moves with the processes) and the second waits; after the first
	// completes with nothing else running and the host still short, the
	// second refuses with the readings named — never a wait on a
	// completion that cannot come.
	available := 2 * gib
	var availMu sync.Mutex
	injectReadings(t, func() (resident.Memory, bool) {
		availMu.Lock()
		defer availMu.Unlock()
		return resident.Memory{TotalBytes: 8 * gib, AvailableBytes: available}, true
	}, passWith(gib))
	c := newAdmission(context.Background(), 4)
	if admitted, _, _ := c.admit(); !admitted {
		t.Fatal("the first package under scarce memory was not admitted")
	}
	availMu.Lock()
	available = gib + gib/2
	availMu.Unlock()
	second := admitAsync(c)
	mustWait(t, second)
	c.release("", 0, 3*gib)
	got := wait(t, second, "refused")
	for _, phrase := range []string{"the host cannot hold one more package process", "available 1.5 GiB", "0 package(s) running", "peak 1.0 GiB", "estimated at 3.0 GiB"} {
		if !strings.Contains(got[1], phrase) {
			t.Fatalf("refusal %q lacks %q (the completed package's 3 GiB peak is the estimate)", got[1], phrase)
		}
	}

	// The live trees' share of the descendants' resident set raises the
	// estimate while packages run: with 2 GiB available, one package
	// admitted, and its tree showing 2.5 GiB, the next package waits —
	// the floor alone would have admitted it.
	share := resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8}
	var shareMu sync.Mutex
	injectReadings(t, hostWith(2*gib), func() (resident.Set, bool) {
		shareMu.Lock()
		defer shareMu.Unlock()
		return share, true
	})
	g := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := g.admit(); !admitted {
		t.Fatalf("the first package was not admitted: %q", refusal)
	}
	shareMu.Lock()
	share.Descendants, share.DescendantsBytes, share.DescendantPeakBytes = 3, 2*gib+gib/2, gib/2
	shareMu.Unlock()
	sharedWaiter := admitAsync(g)
	mustWait(t, sharedWaiter)
	// After the release nothing runs: the share term is inert, the
	// estimate falls back to the floor, and 2 GiB holds one package.
	g.release("", 0, 0)
	wait(t, sharedWaiter, "admitted")

	// A refusal with nothing running comes without waiting, and a live
	// descendant's peak — the pass's own reading — raises the estimate
	// the refusal states above the floor.
	injectReadings(t, hostWith(4*gib), func() (resident.Set, bool) {
		return resident.Set{ProcessBytes: gib / 2, ProcessPeakBytes: gib, Descendants: 1, DescendantsBytes: 2 * gib, DescendantPeakBytes: 5 * gib}, true
	})
	d := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := d.admit(); admitted || !strings.Contains(refusal, "estimated at 5.0 GiB") {
		t.Fatalf("a host that cannot hold one process answered admitted=%v refusal=%q, want a refusal estimating the live descendant's 5 GiB peak", admitted, refusal)
	}

	// The invocation's end releases a waiter unadmitted and unrefused;
	// one held by the processor bound carries no words, one held by the
	// memory term carries the term's.
	injectReadings(t, hostWith(64*gib), passWith(gib))
	ctx, cancel := context.WithCancel(context.Background())
	e := newAdmission(ctx, 1)
	if admitted, _, _ := e.admit(); !admitted {
		t.Fatal("the one slot was not admitted")
	}
	waiter := admitAsync(e)
	mustWait(t, waiter)
	cancel()
	if got := wait(t, waiter, "ended"); got[1] != "" {
		t.Fatalf("a waiter on the processor bound carried words %q at the end, want none", got[1])
	}
	injectReadings(t, hostWith(gib+gib/2), passWith(gib/4))
	ctx2, cancel2 := context.WithCancel(context.Background())
	f := newAdmission(ctx2, 4)
	if admitted, _, _ := f.admit(); !admitted {
		t.Fatal("the first package under the memory term was not admitted")
	}
	heldWaiter := admitAsync(f)
	mustWait(t, heldWaiter)
	cancel2()
	if got := wait(t, heldWaiter, "ended"); !strings.Contains(got[1], "the host cannot hold one more package process") {
		t.Fatalf("a waiter held by the memory term carried %q at the end, want the term's words", got[1])
	}
}

// TestPackageHeldUntilTheInvocationsEndCarriesTheTerm pins the wiring
// of the held words (REQ-evidence-witness-freshness): of two packages
// under readings that hold every ask after the first, the one still
// waiting when the invocation's context ends is the never-spawned run
// carrying the term's words, exactly one.
func TestPackageHeldUntilTheInvocationsEndCarriesTheTerm(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the tree")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	const gib = uint64(1) << 30
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok", "./notest"})
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("held")
	inv.SetTimeout(durationpb.New(time.Minute))
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	invCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var asks atomic.Int32
	injectReadings(t, func() (resident.Memory, bool) {
		if asks.Add(1) == 1 {
			return resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 64 * gib}, true
		}
		// Every later ask finds the host short while the first runs,
		// and the invocation ends on the second's wait.
		go cancel()
		return resident.Memory{TotalBytes: 2 * gib, AvailableBytes: gib / 2}, true
	}, passWith(gib/4))
	runs := runSelectedPackages(ctx, invCtx, n, []string{"example.com/exec/ok", "example.com/exec/notest"}, nil, spawnOrdinals(), nil, nil)
	held := 0
	for _, r := range runs {
		if r.heldBy != "" {
			held++
			if !strings.Contains(r.heldBy, "the host cannot hold one more package process") || !strings.Contains(r.heldBy, "1 package(s) running") {
				t.Fatalf("held run %s carries %q, want the term's words naming the running package", r.pkg, r.heldBy)
			}
		}
	}
	if held != 1 {
		t.Fatalf("%d runs carry the term's words, want exactly the one held at the end: %+v", held, runs)
	}
}

// TestTimedOutPackageHeldByTheMemoryTermNamesIt pins the attribution of
// a package the memory term held until the envelope expired
// (REQ-evidence-witness-freshness): its timeout diagnostic names the
// term and its readings, never a bare timeout.
//
//gofresh:pure
func TestTimedOutPackageHeldByTheMemoryTermNamesIt(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	n := &NormalizedInvocation{Name: "held", Timeout: time.Minute}
	r := packageRun{pkg: "example.com/p", heldBy: "the host cannot hold one more package process beside the pass: available 1.5 GiB"}
	if err := finalizeRun(n, &r, true, ""); err != nil {
		t.Fatal(err)
	}
	if r.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT || len(r.diags) != 1 {
		t.Fatalf("held package finalized as %v with %d diagnostics, want TIMEOUT with one", r.disposition, len(r.diags))
	}
	out := r.diags[0].GetOutput()
	if !strings.Contains(out, "invocation timeout 1m0s expired") || !strings.Contains(out, "held by the memory term: the host cannot hold one more package process beside the pass: available 1.5 GiB") {
		t.Fatalf("held package's diagnostic = %q, want the timeout and the term's words", out)
	}
	plain := packageRun{pkg: "example.com/q"}
	if err := finalizeRun(n, &plain, true, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.diags[0].GetOutput(), "memory term") {
		t.Fatalf("a package held by the processor bound names the memory term: %q", plain.diags[0].GetOutput())
	}
}

// TestRefusedPackageIsolatesNothingOnTheSelectiveForm pins the selective
// form's refusal (REQ-evidence-witness-freshness): a package the host
// cannot hold disposes DEGRADED naming the readings, spawns no process
// and no isolation re-run, and grants no outcome — the re-runs the
// refusal exists to withhold never run, and its witnesses' no-outcome
// cause names the host.
func TestRefusedPackageIsolatesNothingOnTheSelectiveForm(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the tree")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	const gib = uint64(1) << 30
	injectReadings(t, hostWith(gib/2), passWith(gib))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("unholdable-selection")
	inv.SetTimeout(durationpb.New(time.Minute))
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(ctx, n); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	goSpawns := 0
	prior := commandHook
	commandHook = func(name string, args []string) {
		if name == "go" {
			mu.Lock()
			goSpawns++
			mu.Unlock()
		}
	}
	t.Cleanup(func() { commandHook = prior })
	res, err := ExecuteSelection(ctx, n, TestSelection{"example.com/exec/ok": {"TestDouble", "TestSkipped"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tests) != 0 {
		t.Fatalf("a refused package granted outcomes: %v", res.Tests)
	}
	for _, p := range res.Processes {
		if p.Test != "" {
			t.Fatalf("a refused package ran an isolation re-run: %+v", p)
		}
		if p.Package == "example.com/exec/ok" && p.Disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
			t.Fatalf("refused package disposed %v, want DEGRADED", p.Disposition)
		}
	}
	degraded := stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED
	if reason := packageReason(res.Diagnostics, "unholdable-selection", "example.com/exec/ok", degraded); !strings.HasPrefix(reason, "memory: the host cannot hold one more package process") {
		t.Fatalf("refused package's reason = %q, want the memory term's", reason)
	}
	cause := dispositionCause("unholdable-selection", "example.com/exec/ok", degraded, packageReason(res.Diagnostics, "unholdable-selection", "example.com/exec/ok", degraded))
	if !strings.Contains(cause, "degraded: memory: the host cannot hold") {
		t.Fatalf("no-outcome cause = %q, want the disposition with the host's reason", cause)
	}
	mu.Lock()
	defer mu.Unlock()
	if goSpawns != 0 {
		t.Fatalf("a refused selection spawned %d go processes, want none", goSpawns)
	}
}

// TestInvocationTheHostCannotHoldRefusesEveryPackageStated pins the
// refusal end to end (REQ-evidence-witness-freshness): under a host that
// cannot hold one package process beside the pass, every package of the
// invocation disposes DEGRADED with a diagnostic naming the readings,
// and no go process is spawned.
func TestInvocationTheHostCannotHoldRefusesEveryPackageStated(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the tree")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	neutralAmbient(t)
	const gib = uint64(1) << 30
	injectReadings(t, hostWith(gib/2), passWith(gib))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok", "./notest"})
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("unholdable")
	inv.SetTimeout(durationpb.New(time.Minute))
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	// Discovery's own go children are done; the executor's spawns are
	// counted from here.
	var mu sync.Mutex
	goSpawns := 0
	prior := commandHook
	commandHook = func(name string, args []string) {
		if name == "go" {
			mu.Lock()
			goSpawns++
			mu.Unlock()
		}
	}
	t.Cleanup(func() { commandHook = prior })
	health, _, diags, _, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("invocation disposition = %v, want DEGRADED (diags: %v)", got, diags)
	}
	for _, pkg := range []string{"example.com/exec/ok", "example.com/exec/notest"} {
		if got := packageDisposition(t, health, pkg); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
			t.Errorf("package %s = %v, want DEGRADED", pkg, got)
		}
		var named bool
		for _, d := range diags {
			if d.GetPackage() == pkg && strings.Contains(d.GetOutput(), "memory: the host cannot hold one more package process beside the pass") && strings.Contains(d.GetOutput(), "available 512 MiB") {
				named = true
			}
		}
		if !named {
			t.Errorf("package %s carries no diagnostic naming the readings: %v", pkg, diags)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if goSpawns != 0 {
		t.Fatalf("a refused invocation spawned %d go processes, want none", goSpawns)
	}
}

// TestPackageCauseNamesTheRefusalsReason pins the no-outcome cause's
// reason on the selective form (REQ-evidence-witness-freshness): a
// refused package's witnesses are attributed the disposition with the
// package-scoped diagnostic's first line — the host's readings — never a
// test-scoped diagnostic's and never the bare disposition; a timed-out
// package names the memory term when it held the package and the bare
// disposition otherwise.
//
//gofresh:pure
func TestPackageCauseNamesTheRefusalsReason(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	m := newExecMerge()
	m.pkgDisp[invPkgKey("inv", "example.com/p")] = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED
	solo := &stipulatorv1.FailureDiagnostic{}
	solo.SetInvocation("inv")
	solo.SetPackage("example.com/p")
	solo.SetTest("TestOne")
	solo.SetOutput("a solo's own failure")
	// A stream diagnostic of the package precedes the degradation's
	// own, which the classifier appends last: the cause reads the
	// diagnostic carrying the package's disposition, never the first.
	stream := &stipulatorv1.FailureDiagnostic{}
	stream.SetInvocation("inv")
	stream.SetPackage("example.com/p")
	stream.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	stream.SetOutput("ordinary package output the stream carried")
	pkg := &stipulatorv1.FailureDiagnostic{}
	pkg.SetInvocation("inv")
	pkg.SetPackage("example.com/p")
	pkg.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	pkg.SetOutput("memory: the host cannot hold one more package process beside the pass: available 512 MiB\nsecond line")
	m.diags = append(m.diags, solo, stream, pkg)
	cause, ok := m.packageCause("inv", "example.com/p")
	want := "invocation inv: package example.com/p degraded: memory: the host cannot hold one more package process beside the pass: available 512 MiB"
	if !ok || cause != want {
		t.Fatalf("packageCause = %q %v, want %q", cause, ok, want)
	}
	if cause, ok := m.packageCause("inv", "example.com/other"); ok || cause != "" {
		t.Fatalf("an unrecorded package answered %q %v", cause, ok)
	}
	timeout := stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT
	m.pkgDisp[invPkgKey("inv", "example.com/plain")] = timeout
	plain := &stipulatorv1.FailureDiagnostic{}
	plain.SetInvocation("inv")
	plain.SetPackage("example.com/plain")
	plain.SetDisposition(timeout)
	plain.SetOutput("invocation timeout 8s expired before the package completed\nstarted but unfinished: TestSlow")
	m.pkgDisp[invPkgKey("inv", "example.com/held")] = timeout
	held := &stipulatorv1.FailureDiagnostic{}
	held.SetInvocation("inv")
	held.SetPackage("example.com/held")
	held.SetDisposition(timeout)
	held.SetOutput("invocation timeout 8s expired before the package completed\nheld by the memory term: the host cannot hold one more package process beside the pass: available 1.5 GiB")
	m.diags = append(m.diags, plain, held)
	// A reason line is bounded.
	m.pkgDisp[invPkgKey("inv", "example.com/long")] = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED
	long := &stipulatorv1.FailureDiagnostic{}
	long.SetInvocation("inv")
	long.SetPackage("example.com/long")
	long.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	long.SetOutput(strings.Repeat("x", 2*packageReasonBound))
	m.diags = append(m.diags, long)
	if cause, _ := m.packageCause("inv", "example.com/long"); len(cause) > packageReasonBound+len("invocation inv: package example.com/long degraded: ")+4 {
		t.Fatalf("a long reason was not bounded: %d chars", len(cause))
	}
	if cause, _ := m.packageCause("inv", "example.com/plain"); cause != "invocation inv: package example.com/plain timeout" {
		t.Fatalf("a plain timeout's cause = %q, want the bare disposition", cause)
	}
	if cause, _ := m.packageCause("inv", "example.com/held"); cause != "invocation inv: package example.com/held timeout: held by the memory term: the host cannot hold one more package process beside the pass: available 1.5 GiB" {
		t.Fatalf("a held timeout's cause = %q, want the term named", cause)
	}
}
