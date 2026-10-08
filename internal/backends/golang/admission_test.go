package golang

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/greatliontech/gofresh/resident"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// injectReadings installs synthetic host and pass readings for the
// admission for the test's life.
func injectReadings(t *testing.T, host func() (resident.Memory, bool), sample func() (resident.Set, bool)) {
	t.Helper()
	freshGate(t)
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
	freshGate(t)
	prior := readingsHook
	readingsHook = func() (resident.Reading, bool) { return read(), true }
	t.Cleanup(func() { readingsHook = prior })
}

// freshGate gives the test its own host gate: a unit pin's admissions
// run no invocation, so nothing leaves the gate for them; an admission
// captures the gate at its construction, so the swap is race-free.
func freshGate(t *testing.T) {
	t.Helper()
	prior := theHostGate
	theHostGate = newHostGate()
	t.Cleanup(func() { theHostGate = prior })
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
	a.release()
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
	// process table, are reserved their estimate — with 2.5 GiB
	// available and the 1 GiB floor, two are admitted on the same
	// reading and the third waits, whatever the processor bound.
	injectReadings(t, hostWith(5*gib/2), passWith(gib/4))
	burst := newAdmission(context.Background(), 16)
	for i := 0; i < 2; i++ {
		if admitted, refusal, _ := burst.admit(); !admitted {
			t.Fatalf("package %d of the burst was not admitted: %q", i, refusal)
		}
	}
	third := admitAsync(burst)
	mustWait(t, third)
	burst.release()
	wait(t, third, "admitted")

	// The reservation is per tree, never netted across trees: four
	// running packages, one tree grown to 8 GiB of small processes and
	// three just-admitted siblings showing nothing, 3 GiB available —
	// the estimate is the grown tree's observed 8 GiB, the three bare
	// siblings reserve it each, and a fifth package waits; netting the
	// grown tree's bytes against its siblings would have admitted it.
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
		trees.spawned("", pid)
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
	// observed 8 GiB (its share too) is the estimate, and 3 GiB still
	// holds nothing more — the fifth keeps waiting.
	trees.reaped("", 12, 0)
	trees.release()
	trees.reaped("", 13, 0)
	trees.release()
	trees.reaped("", 14, 0)
	trees.release()
	mustWait(t, fifth)
	// The grown tree completes: nothing runs, the share and the
	// observed tree maxima are gone with the registrations, the
	// estimate falls to the floor, and 3 GiB holds one — admitted.
	currentMu.Lock()
	current.Set.Descendants, current.Set.DescendantsBytes, current.Trees = 0, 0, nil
	currentMu.Unlock()
	trees.reaped("", 11, 0)
	trees.release()
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
	c.reaped("", 0, 3*gib)
	c.release()
	got := wait(t, second, "refused")
	for _, phrase := range []string{"the host cannot hold one more package process", "available 1.5 GiB", "0 package(s) running", "this phase's peak 512 MiB", "estimated at 3.0 GiB"} {
		if !strings.Contains(got[1], phrase) {
			t.Fatalf("refusal %q lacks %q (the completed package's 3 GiB peak is the estimate)", got[1], phrase)
		}
	}

	// A registered tree's observed set raises the estimate while
	// packages run: with 2 GiB available, one package admitted and its
	// tree showing 2.5 GiB, the next package waits — the floor alone
	// would have admitted it.
	share := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 4 * gib, AvailableBytes: 2 * gib},
	}
	var shareMu sync.Mutex
	injectReading(t, func() resident.Reading {
		shareMu.Lock()
		defer shareMu.Unlock()
		return share
	})
	g := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := g.admit(); !admitted {
		t.Fatalf("the first package was not admitted: %q", refusal)
	}
	g.spawned("", 31)
	shareMu.Lock()
	share.Set.Descendants, share.Set.DescendantsBytes, share.Set.DescendantPeakBytes = 3, 2*gib+gib/2, gib/2
	share.Trees = map[int]uint64{31: 2*gib + gib/2}
	shareMu.Unlock()
	sharedWaiter := admitAsync(g)
	mustWait(t, sharedWaiter)
	// After the release nothing runs: the observation left with the
	// registration, the estimate falls back to the floor, and 2 GiB
	// holds one package.
	g.reaped("", 31, 0)
	g.release()
	wait(t, sharedWaiter, "admitted")

	// A refusal with nothing running comes without waiting, and a
	// completed package's peak raises the estimate the refusal states
	// above the floor — a live descendant outside every registered tree
	// (here one with a 5 GiB peak) raises nothing: the first ask is
	// admitted beside it, and only the completed 5 GiB refuses the next.
	injectReadings(t, hostWith(4*gib), func() (resident.Set, bool) {
		return resident.Set{ProcessBytes: gib / 2, ProcessPeakBytes: gib, Descendants: 1, DescendantsBytes: 2 * gib, DescendantPeakBytes: 5 * gib}, true
	})
	d := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := d.admit(); !admitted {
		t.Fatalf("a package beside a 5 GiB descendant outside every registered tree was refused: %q", refusal)
	}
	d.reaped("", 0, 5*gib)
	d.release()
	if admitted, refusal, _ := d.admit(); admitted || !strings.Contains(refusal, "estimated at 5.0 GiB") {
		t.Fatalf("a host that cannot hold one process answered admitted=%v refusal=%q, want a refusal estimating the completed package's 5 GiB peak", admitted, refusal)
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
	// The first admitted package is a slow one or a quick one by the
	// goroutines' race; the second asks within microseconds of the
	// first's spawn, and the cancellation is synchronous inside that
	// ask, so the first never completes before the second is held.
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./sleepy", "./ok"})
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("held")
	inv.SetTimeout(durationpb.New(time.Minute))
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	// The pin witnesses the MEMORY term: with one processor slot (the
	// self-host check's children run two processors wide, so the
	// derived bound is one) the second package would wait on the
	// processor bound, never ask, and be refused after the first
	// completes — so the bound is two here.
	n.SpawnBound = 2
	invCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var asks atomic.Int32
	injectReadings(t, func() (resident.Memory, bool) {
		if asks.Add(1) == 1 {
			return resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 64 * gib}, true
		}
		// Every later ask finds the host short while the first runs,
		// and the invocation ends on the second's wait: cancelled here,
		// before the ask returns, so the waiter sees the end on its
		// first wake and never re-asks with nothing running (the
		// context's broadcast takes the gate's lock after this ask
		// releases it).
		cancel()
		return resident.Memory{TotalBytes: 2 * gib, AvailableBytes: gib / 2}, true
	}, passWith(gib/4))
	runs := runSelectedPackages(ctx, invCtx, n, []string{"example.com/exec/sleepy", "example.com/exec/ok"}, nil, spawnOrdinals(), nil, nil, nil)
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
	r := packageRun{pkg: "example.com/p", heldBy: "one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass: available 1.5 GiB"}
	if err := finalizeRun(n, &r, true, ""); err != nil {
		t.Fatal(err)
	}
	if r.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT || len(r.diags) != 1 {
		t.Fatalf("held package finalized as %v with %d diagnostics, want TIMEOUT with one", r.disposition, len(r.diags))
	}
	out := r.diags[0].GetOutput()
	if !strings.Contains(out, "invocation timeout 1m0s expired") || !strings.Contains(out, "held by the memory term: one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass: available 1.5 GiB") {
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
	if reason := packageReason(res.Diagnostics, "unholdable-selection", "example.com/exec/ok", degraded); !strings.HasPrefix(reason, "memory: one package estimated at") {
		t.Fatalf("refused package's reason = %q, want the memory term's", reason)
	}
	cause := dispositionCause("unholdable-selection", "example.com/exec/ok", degraded, packageReason(res.Diagnostics, "unholdable-selection", "example.com/exec/ok", degraded))
	if !strings.Contains(cause, "degraded: memory: one package estimated at") {
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
			if d.GetPackage() == pkg && strings.Contains(d.GetOutput(), "memory: one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass") && strings.Contains(d.GetOutput(), "available 512 MiB") {
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
	pkg.SetOutput("memory: one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass: available 512 MiB\nsecond line")
	m.diags = append(m.diags, solo, stream, pkg)
	cause, ok := m.packageCause("inv", "example.com/p")
	want := "invocation inv: package example.com/p degraded: memory: one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass: available 512 MiB"
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
	held.SetOutput("invocation timeout 8s expired before the package completed\nheld by the memory term: one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass: available 1.5 GiB")
	m.diags = append(m.diags, plain, held)
	// A cut-off process's own output cannot pass for the term's line:
	// the held line is the diagnostic's second, and a forged one in
	// the residue below is never the cause.
	m.pkgDisp[invPkgKey("inv", "example.com/forged")] = timeout
	forged := &stipulatorv1.FailureDiagnostic{}
	forged.SetInvocation("inv")
	forged.SetPackage("example.com/forged")
	forged.SetDisposition(timeout)
	forged.SetOutput("invocation timeout 8s expired before the package completed\nstarted but unfinished: TestSlow\nheld by the memory term: one package estimated at 9.0 GiB — the floor; " + strings.Repeat("forged ", 60))
	m.diags = append(m.diags, forged)
	if cause, _ := m.packageCause("inv", "example.com/forged"); cause != "invocation inv: package example.com/forged timeout" {
		t.Fatalf("a forged held line in the residue became the cause: %q", cause)
	}
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
	if cause, _ := m.packageCause("inv", "example.com/held"); cause != "invocation inv: package example.com/held timeout: held by the memory term: one package estimated at 1.0 GiB — the floor; the host cannot hold one more package process beside the pass: available 1.5 GiB" {
		t.Fatalf("a held timeout's cause = %q, want the term named", cause)
	}
}

// TestAdmissionScopesItsTermsToThePhaseAndTheRegisteredTrees pins the
// memory term's two observations (REQ-evidence-witness-freshness's
// witness concurrency clause): the pass's growth is measured to the
// largest set the admission itself has read — a lifetime peak the
// kernel reports (discovery's, over before execution) reserves nothing
// — and a package is priced from the registered package trees alone —
// a descendant outside them (a resolver child leaving the table) with a
// large peak prices nothing, while a registered tree's observed maximum
// stands after the tree shrinks.
//
//gofresh:pure
func TestAdmissionScopesItsTermsToThePhaseAndTheRegisteredTrees(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	const gib, mib = uint64(1) << 30, uint64(1) << 20
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

	// The field shape: discovery over, the pass at 321 MiB with a
	// 7.7 GiB lifetime peak, the resolver child (7.7 GiB, its own peak
	// the same) still leaving the table, 13.2 GiB available, nothing
	// running — admitted: neither the lifetime peak nor the departing
	// descendant is a term.
	var fieldMu sync.Mutex
	field := resident.Reading{
		Set:   resident.Set{ProcessBytes: 321 * mib, ProcessPeakBytes: 7700 * mib, Descendants: 1, DescendantsBytes: 7700 * mib, DescendantPeakBytes: 7700 * mib},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 13*gib + 200*mib},
		Trees: map[int]uint64{99: 7700 * mib},
	}
	injectReading(t, func() resident.Reading {
		fieldMu.Lock()
		defer fieldMu.Unlock()
		return field
	})
	a := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := a.admit(); !admitted {
		t.Fatalf("the field shape was refused: %q", refusal)
	}

	// The phase's own peak: a pass that showed 1.5 GiB to this
	// admission and fell to 512 MiB reserves the 1 GiB back — with
	// 1.5 GiB available nothing more fits beside the floor, and with
	// nothing running the ask refuses naming this phase's peak; the
	// kernel's 100 GiB lifetime mark is never the reference.
	var phaseMu sync.Mutex
	phase := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib + gib/2, ProcessPeakBytes: 100 * gib},
		Host: resident.Memory{TotalBytes: 8 * gib, AvailableBytes: gib + gib/2},
	}
	injectReading(t, func() resident.Reading {
		phaseMu.Lock()
		defer phaseMu.Unlock()
		return phase
	})
	b := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := b.admit(); !admitted {
		t.Fatalf("the first package under the phase's own set was refused: %q", refusal)
	}
	b.release()
	phaseMu.Lock()
	phase.Set.ProcessBytes = gib / 2
	phaseMu.Unlock()
	if admitted, refusal, _ := b.admit(); admitted || !strings.Contains(refusal, "this phase's peak 1.5 GiB") {
		t.Fatalf("the pass's room to grow back to this phase's peak was not reserved: admitted=%v refusal=%q", admitted, refusal)
	}

	// A registered tree's observed maximum stands after the tree
	// shrinks: with 1.6 GiB available the first package is admitted and
	// registered, a second is admitted while the tree shows 1.5 GiB
	// (the available memory holds it), and after the second completes with the
	// tree down to 512 MiB a third waits — the tree is priced at the
	// 1.5 GiB it showed, reserving the 1 GiB it may grow back, which the
	// current bytes alone would have admitted; the tree's completion
	// releases the observation and admits the third.
	var treeMu sync.Mutex
	tree := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 8 * gib, AvailableBytes: 8 * gib / 5},
	}
	injectReading(t, func() resident.Reading {
		treeMu.Lock()
		defer treeMu.Unlock()
		return tree
	})
	c := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := c.admit(); !admitted {
		t.Fatalf("the first package was refused: %q", refusal)
	}
	c.spawned("", 21)
	treeMu.Lock()
	tree.Trees = map[int]uint64{21: gib + gib/2}
	treeMu.Unlock()
	if admitted, refusal, _ := c.admit(); !admitted {
		t.Fatalf("the second package beside a 1.5 GiB tree was refused: %q", refusal)
	}
	treeMu.Lock()
	tree.Trees = map[int]uint64{21: gib / 2}
	treeMu.Unlock()
	c.release()
	third := admitAsync(c)
	mustWait(t, third)
	c.reaped("", 21, 0)
	c.release()
	wait(t, third, "admitted")
	// A reused pid starts its observation afresh: with 2 GiB available
	// a package's tree is shown at 1.5 GiB (a second is admitted beside
	// it), the package completes and the pid is registered for the
	// second's process, shown at 256 MiB — the released tree's maximum
	// left with its registration, so a third is admitted beside the
	// floor; the stale 1.5 GiB would have held it.
	var reuseMu sync.Mutex
	reuse := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 8 * gib, AvailableBytes: 2 * gib},
	}
	injectReading(t, func() resident.Reading {
		reuseMu.Lock()
		defer reuseMu.Unlock()
		return reuse
	})
	r := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := r.admit(); !admitted {
		t.Fatalf("the first package was refused: %q", refusal)
	}
	r.spawned("", 21)
	reuseMu.Lock()
	reuse.Trees = map[int]uint64{21: gib + gib/2}
	reuseMu.Unlock()
	if admitted, refusal, _ := r.admit(); !admitted {
		t.Fatalf("the second package beside a 1.5 GiB tree was refused: %q", refusal)
	}
	r.reaped("", 21, 0)
	r.release()
	r.spawned("", 21)
	reuseMu.Lock()
	reuse.Trees = map[int]uint64{21: gib / 4}
	reuseMu.Unlock()
	wait(t, admitAsync(r), "admitted")

	// A stranger's bytes in the descendants' sum price nothing: one
	// package running with its registered tree at 512 MiB while the
	// descendants' sum reads 8 GiB (the pass's own drivers, a child
	// leaving the table) — the next package is admitted with 2 GiB
	// available; the descendants' share would have held it.
	var strangerMu sync.Mutex
	stranger := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 8 * gib, AvailableBytes: 2 * gib},
	}
	injectReading(t, func() resident.Reading {
		strangerMu.Lock()
		defer strangerMu.Unlock()
		return stranger
	})
	d := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := d.admit(); !admitted {
		t.Fatalf("the first package was refused: %q", refusal)
	}
	d.spawned("", 41)
	strangerMu.Lock()
	stranger.Set.Descendants, stranger.Set.DescendantsBytes = 9, 8*gib
	stranger.Trees = map[int]uint64{41: gib / 2}
	strangerMu.Unlock()
	wait(t, admitAsync(d), "admitted")
}

// TestEstimateNamesItsOrigin pins REQ-evidence-admission-origin's first
// half over synthetic readings: the words the term speaks name the
// estimate's origin — the floor before any process showed a peak; the
// package and process whose completed peak the estimate is; the
// package whose live tree the readings show larger than any peak —
// and every face carries the words as the term spoke them.
//
//gofresh:pure
func TestEstimateNamesItsOrigin(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-admission-origin")
	const gib = uint64(1) << 30
	// A host holding less than the floor beside a small pass, nothing
	// running: refused naming the floor.
	injectReadings(t, hostWith(gib/2), passWith(gib/8))
	a := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := a.admit(); admitted || !strings.Contains(refusal, "one package estimated at 1.0 GiB — the floor") {
		t.Fatalf("under the floor: admitted=%v refusal=%q; want the floor named", admitted, refusal)
	}
	// A completed process's peak raises the estimate and names the
	// package and the process.
	a.reaped("example.com/big", 4242, 3*gib)
	if admitted, refusal, _ := a.admit(); admitted || !strings.Contains(refusal, "one package estimated at 3.0 GiB — package example.com/big's completed process 4242's peak") {
		t.Fatalf("after a completed peak: admitted=%v refusal=%q; want the package and process named", admitted, refusal)
	}
	// A registered live tree larger than every peak names its package
	// and process: admitted under a roomy host, registered, then the
	// reading shows the tree at 5 GiB with the room gone.
	var mu sync.Mutex
	reading := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 64 * gib},
	}
	injectReading(t, func() resident.Reading {
		mu.Lock()
		defer mu.Unlock()
		return reading
	})
	a.leave()
	b := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := b.admit(); !admitted {
		t.Fatalf("the roomy host refused: %q", refusal)
	}
	b.spawned("example.com/live", 77)
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 2 * gib},
		Trees: map[int]uint64{77: 5 * gib},
	}
	mu.Unlock()
	second := admitAsync(b)
	select {
	case got := <-second:
		t.Fatalf("beside a 5 GiB live tree with 2 GiB available the second package was answered %v, want it held", got)
	case <-time.After(100 * time.Millisecond):
	}
	// The held words are the term's: end the invocation and read them.
	b.reaped("example.com/live", 77, 0)
	b.release()
	select {
	case got := <-second:
		if got[0] != "admitted" {
			t.Fatalf("after the live tree's package ended the second package was %v, want admitted", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second package was not admitted after the release")
	}
	// The live tree's words, read at the ask under a held context: a
	// third admission whose only registered tree is large.
	ctx, cancel := context.WithCancel(context.Background())
	b.leave()
	c := newAdmission(ctx, 4)
	mu.Lock()
	reading.Host.AvailableBytes = 64 * gib
	mu.Unlock()
	if admitted, refusal, _ := c.admit(); !admitted {
		t.Fatalf("the roomy host refused: %q", refusal)
	}
	c.spawned("example.com/live", 78)
	// The reading signals each time the 5 GiB tree is served, so the
	// cancellation follows an ask that read it — deterministic.
	served := make(chan struct{}, 16)
	injectReading(t, func() resident.Reading {
		mu.Lock()
		defer mu.Unlock()
		select {
		case served <- struct{}{}:
		default:
		}
		return reading
	})
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 2 * gib},
		Trees: map[int]uint64{78: 5 * gib},
	}
	mu.Unlock()
	third := admitAsync(c)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the held package never asked")
	}
	// A reap re-asks the held package (the readings moved): the live
	// tree's process ends with a 6 GiB peak, and the words the package
	// holds name the completed process now, the reaped peak pricing
	// the next spawn at once.
	c.reaped("example.com/live", 78, 6*gib)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the held package did not re-ask after the reap")
	}
	cancel()
	select {
	case got := <-third:
		if got[0] != "ended" || !strings.Contains(got[1], "one package estimated at 6.0 GiB — package example.com/live's completed process 78's peak") {
			t.Fatalf("the held package's words = %v, want the reaped process's peak named after the re-ask", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held package did not end with the invocation")
	}
	// Two registered trees showing one largest reading: the words name
	// the lowest process, whichever order the table lists them in.
	ctx5, cancel5 := context.WithCancel(context.Background())
	c.leave()
	tie := newAdmission(ctx5, 4)
	mu.Lock()
	reading.Host.AvailableBytes = 64 * gib
	mu.Unlock()
	for i := 0; i < 2; i++ {
		if admitted, refusal, _ := tie.admit(); !admitted {
			t.Fatalf("the roomy host refused: %q", refusal)
		}
	}
	tie.spawned("example.com/nine", 9)
	tie.spawned("example.com/five", 5)
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 2 * gib},
		Trees: map[int]uint64{9: 5 * gib, 5: 5 * gib},
	}
	mu.Unlock()
	for len(served) > 0 {
		<-served
	}
	tied := admitAsync(tie)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the tied admission's held package never asked")
	}
	cancel5()
	select {
	case got := <-tied:
		if got[0] != "ended" || !strings.Contains(got[1], "one package estimated at 5.0 GiB — package example.com/five's live tree (process 5) in this invocation's readings") {
			t.Fatalf("the tied trees' words = %v, want the lowest process's live tree named whole", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tied admission's held package did not end with the invocation")
	}
	// The words reach the witnesses' bounded cause whole in their
	// deciding part: the held package's timeout diagnostic carries the
	// term's line, and packageReason's bounded cut keeps the estimate
	// and its origin under a long package path.
	long := strings.Repeat("github.com/example/organisation/", 2) + "internal/compile/joints"
	ctxE, cancelE := context.WithCancel(context.Background())
	tie.leave()
	e := newAdmission(ctxE, 4)
	e.reaped(long, 31337, 7*gib+gib/3)
	mu.Lock()
	reading = resident.Reading{
		Set:  resident.Set{ProcessBytes: 675 * (gib / 1024), ProcessPeakBytes: 679 * (gib / 1024)},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 12*gib + 300*(gib/1024)},
	}
	mu.Unlock()
	// The field shape: one package running reserving the 7.3 GiB
	// estimate, 12.3 GiB available — the next package is held.
	if admitted, refusal, _ := e.admit(); !admitted {
		t.Fatalf("the field shape's first package was refused: %q", refusal)
	}
	for len(served) > 0 {
		<-served
	}
	fifth := admitAsync(e)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the field shape's held package never asked")
	}
	cancelE()
	var words string
	select {
	case got := <-fifth:
		if got[0] != "ended" {
			t.Fatalf("the field shape's second package was %v, want held to the end", got)
		}
		words = got[1]
	case <-time.After(5 * time.Second):
		t.Fatal("the field shape's held package did not end with the invocation")
	}
	n := &NormalizedInvocation{Name: "held", Timeout: time.Minute}
	r := packageRun{pkg: long, heldBy: words}
	if err := finalizeRun(n, &r, true, ""); err != nil {
		t.Fatal(err)
	}
	cause := packageReason(r.diags, n.Name, long, stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT)
	if !strings.Contains(cause, "one package estimated at 7.3 GiB — package "+long+"'s completed process 31337's peak") {
		t.Fatalf("the witnesses' bounded cause lost the origin: %q", cause)
	}
	if len([]rune(cause)) > len([]rune(heldByPrefix))+len([]rune("one package estimated at 7.3 GiB — package "+long+"'s completed process 31337's peak; "))+packageReasonBound {
		t.Fatalf("the readings after the origin are unbounded: %d runes", len([]rune(cause)))
	}
	// A live-tree origin under a longer path — its tail the longest the
	// words have — survives the bound whole too.
	longer := strings.Repeat("github.com/example/organisation/", 3) + "internal/compile/joints"
	ctxL, cancelL := context.WithCancel(context.Background())
	e.leave()
	l := newAdmission(ctxL, 4)
	mu.Lock()
	reading.Host.AvailableBytes = 64 * gib
	mu.Unlock()
	if admitted, refusal, _ := l.admit(); !admitted {
		t.Fatalf("the roomy host refused: %q", refusal)
	}
	l.spawned(longer, 4321)
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 2 * gib},
		Trees: map[int]uint64{4321: 5 * gib},
	}
	mu.Unlock()
	for len(served) > 0 {
		<-served
	}
	sixth := admitAsync(l)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the long-path admission's held package never asked")
	}
	cancelL()
	var liveWords string
	select {
	case got := <-sixth:
		if got[0] != "ended" {
			t.Fatalf("the long-path held package was %v, want held to the end", got)
		}
		liveWords = got[1]
	case <-time.After(5 * time.Second):
		t.Fatal("the long-path held package did not end with the invocation")
	}
	rl := packageRun{pkg: longer, heldBy: liveWords}
	if err := finalizeRun(n, &rl, true, ""); err != nil {
		t.Fatal(err)
	}
	if cause := packageReason(rl.diags, n.Name, longer, stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT); !strings.Contains(cause, "package "+longer+"'s live tree (process 4321) in this invocation's readings; ") {
		t.Fatalf("the witnesses' bounded cause cut a live-tree origin under a long path: %q", cause)
	}
	l.leave()
}

// TestBoundedReasonKeepsTheOriginWhole pins the no-outcome cause's
// bound over the memory term's line: the estimate and its origin —
// everything before the first "; " — are kept whole however long the
// package path, the readings after it are cut at the bound, and a line
// that is not the term's is cut whole (REQ-evidence-admission-origin).
//
//gofresh:pure
func TestBoundedReasonKeepsTheOriginWhole(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-admission-origin")
	head := "held by the memory term: one package estimated at 7.3 GiB — package " + strings.Repeat("github.com/example/organisation/", 4) + "joints's live tree (process 4321) in this invocation's readings"
	rest := strings.Repeat("the host cannot hold one more package process beside the pass; ", 8)
	got := boundedReason(head + "; " + rest)
	if !strings.HasPrefix(got, head+"; ") {
		t.Fatalf("the term's head was cut: %q", got)
	}
	if tail := strings.TrimPrefix(got, head+"; "); len([]rune(tail)) > packageReasonBound || !strings.HasPrefix(rest, strings.TrimSuffix(tail, "…")) {
		t.Fatalf("the readings after the origin were not cut at the bound: %d runes, %q", len([]rune(tail)), tail)
	}
	impostor := "stream: " + strings.Repeat("one package estimated at 9.0 GiB — the floor; ", 8)
	if got := boundedReason(impostor); len([]rune(got)) > packageReasonBound+1 {
		t.Fatalf("a line carrying the term's words without its prefix kept more than the bound: %d runes", len([]rune(got)))
	}
	plain := strings.Repeat("spawning go test: a long environmental refusal; ", 8)
	if got := boundedReason(plain); len([]rune(got)) > packageReasonBound+1 || !strings.HasPrefix(plain, strings.TrimSuffix(got, "…")) {
		t.Fatalf("a plain line was not cut whole at the bound: %d runes", len([]rune(got)))
	}
}

// TestAdmissionsOfOneProcessShareTheHostGate pins
// REQ-evidence-admission-origin's gate sentence: two admissions alive
// in one process — two concurrent operations — are judged together
// over one reading; the second's room is the host's less the first's
// running packages' reservation, so a package the first could hold
// alone is held beside it — never refused, the first's release can
// come — its words naming the other operations' packages, admitted
// when the first's release frees a slot (the release wakes it) — and
// a third held beside two operations' packages admitted by the
// first's end.
//
//gofresh:pure
func TestAdmissionsOfOneProcessShareTheHostGate(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-admission-origin")
	const gib = uint64(1) << 30
	// 2.5 GiB available, a small pass, the 1 GiB floor: one operation
	// holds two packages (its second reserves the whole estimate).
	injectReadings(t, hostWith(5*gib/2), passWith(gib/8))
	first := newAdmission(context.Background(), 8)
	t.Cleanup(func() { first.leave() })
	for i := 0; i < 2; i++ {
		if admitted, refusal, _ := first.admit(); !admitted {
			t.Fatalf("the first operation's package %d was refused: %q", i, refusal)
		}
	}
	// A second operation over the same host: alone it would hold two
	// packages too; beside the first's 2 GiB reservation it holds none
	// — HELD (the first's release can come), its words naming the
	// others' reservation, never refused as a package nothing can free.
	ctx2, cancel2 := context.WithCancel(context.Background())
	second := newAdmission(ctx2, 8)
	t.Cleanup(func() { second.leave() })
	heldAsk := admitAsync(second)
	select {
	case got := <-heldAsk:
		t.Fatalf("beside the first operation the second was answered %v, want it held", got)
	case <-time.After(100 * time.Millisecond):
	}
	// The first operation releases one package: the release alone
	// wakes the second's waiter, and the second admits into the room it
	// freed.
	first.release()
	select {
	case got := <-heldAsk:
		if got[0] != "admitted" {
			t.Fatalf("after the first released one package the second was %v, want admitted", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first operation's release did not wake the second")
	}
	// A third operation's ask beside the first's one package and the
	// second's one is held; the first operation's end — its leave —
	// wakes it and it admits.
	third := newAdmission(ctx2, 8)
	t.Cleanup(func() { third.leave() })
	thirdAsk := admitAsync(third)
	select {
	case got := <-thirdAsk:
		t.Fatalf("beside two operations' packages the third was answered %v, want it held", got)
	case <-time.After(100 * time.Millisecond):
	}
	first.leave()
	select {
	case got := <-thirdAsk:
		if got[0] != "admitted" {
			t.Fatalf("after the first operation left the gate the third was %v, want admitted", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first operation's end did not wake the third")
	}
	// The held words name the others' reservation: a fourth operation's
	// ask beside the second's and the third's packages, cancelled while
	// held.
	fourth := newAdmission(ctx2, 8)
	t.Cleanup(func() { fourth.leave() })
	fourthAsk := admitAsync(fourth)
	time.Sleep(50 * time.Millisecond)
	cancel2()
	select {
	case got := <-fourthAsk:
		if got[0] != "ended" || !strings.Contains(got[1], "this process's other operations running 2 package(s) reserving 2.0 GiB beyond the 0 B their trees show") {
			t.Fatalf("the held package's words = %v, want the others' two packages and 2 GiB named", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the held fourth ask did not end with its invocation")
	}

	// Judged over one reading, never a published figure: the first
	// operation's tree, registered at 3 GiB with 1 GiB available, shrinks
	// to 0.5 GiB with 3.5 GiB available — by the first's own rule its
	// package still reserves 2.5 GiB, so a second operation admits one
	// floor package and holds the next (a snapshot taken at the first's
	// last judgment would have read 0 and admitted three).
	freshGate(t)
	var mu sync.Mutex
	reading := resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 4 * gib},
	}
	injectReading(t, func() resident.Reading {
		mu.Lock()
		defer mu.Unlock()
		return reading
	})
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	alpha := newAdmission(ctxA, 2)
	if admitted, refusal, _ := alpha.admit(); !admitted {
		t.Fatalf("alpha's first package was refused: %q", refusal)
	}
	alpha.spawned("example.com/alpha", 101)
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: gib},
		Trees: map[int]uint64{101: 3 * gib},
	}
	mu.Unlock()
	alphaSecond := admitAsync(alpha)
	select {
	case got := <-alphaSecond:
		t.Fatalf("alpha's second package beside its 3 GiB tree was answered %v, want held", got)
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 3*gib + gib/2},
		Trees: map[int]uint64{101: gib / 2},
	}
	mu.Unlock()
	beta := newAdmission(ctxA, 8)
	if admitted, refusal, _ := beta.admit(); !admitted {
		t.Fatalf("beta's first package beside alpha's shrunk tree was refused: %q", refusal)
	}
	betaSecond := admitAsync(beta)
	select {
	case got := <-betaSecond:
		t.Fatalf("beta's second package was answered %v, want held: alpha's tree still reserves 2.5 GiB by alpha's own rule", got)
	case <-time.After(100 * time.Millisecond):
	}
	// Refused only when nothing of the process runs: gamma, with
	// nothing running, asks beside alpha's running package whose tree
	// shows its whole estimate (a reservation of nothing) — held, since
	// alpha's completion frees the tree's held pages; its words name
	// alpha's running package.
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: gib / 2},
		Trees: map[int]uint64{101: 3 * gib},
	}
	mu.Unlock()
	ctxG, cancelG := context.WithCancel(context.Background())
	gamma := newAdmission(ctxG, 8)
	gammaAsk := admitAsync(gamma)
	select {
	case got := <-gammaAsk:
		t.Fatalf("gamma beside alpha's running package was answered %v, want held", got)
	case <-time.After(100 * time.Millisecond):
	}
	cancelG()
	select {
	case got := <-gammaAsk:
		if got[0] != "ended" || !strings.Contains(got[1], "this process's other operations running") {
			t.Fatalf("gamma's held words = %v, want the others' running packages named", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gamma's held ask did not end with its invocation")
	}
	cancelA()
	// The growth term is the process's: a later operation's admission
	// reserves the room back to the largest set ANY admission of the
	// process has read — delta saw the pass at 1.5 GiB, epsilon, minted
	// after it fell to 512 MiB, must leave the 1 GiB of growth over, so
	// with 1.5 GiB available and the floor it is refused (nothing of the
	// process runs) naming the process's peak.
	freshGate(t)
	mu.Lock()
	reading = resident.Reading{
		Set:  resident.Set{ProcessBytes: gib + gib/2, ProcessPeakBytes: 100 * gib},
		Host: resident.Memory{TotalBytes: 8 * gib, AvailableBytes: 8 * gib},
	}
	mu.Unlock()
	injectReading(t, func() resident.Reading {
		mu.Lock()
		defer mu.Unlock()
		return reading
	})
	delta := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := delta.admit(); !admitted {
		t.Fatalf("delta under a roomy host was refused: %q", refusal)
	}
	delta.release()
	mu.Lock()
	reading = resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 2, ProcessPeakBytes: 100 * gib},
		Host: resident.Memory{TotalBytes: 8 * gib, AvailableBytes: gib + gib/2},
	}
	mu.Unlock()
	epsilon := newAdmission(context.Background(), 4)
	if admitted, refusal, _ := epsilon.admit(); admitted || !strings.Contains(refusal, "this phase's peak 1.5 GiB") {
		t.Fatalf("epsilon beside delta's 1.5 GiB pass peak: admitted=%v refusal=%q; want refused naming the process's peak", admitted, refusal)
	}
	epsilon.leave()
	delta.leave()
	// A context ending — a timeout, a cancellation — does not end the
	// membership: zeta's context is cancelled while its package still
	// runs (its tree in the table), and eta, with nothing running, is
	// HELD beside it — the dying tree is reaped and released in time —
	// its words naming zeta's running package; zeta's reap, release
	// and leave admit eta.
	freshGate(t)
	mu.Lock()
	reading = resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 64 * gib},
	}
	mu.Unlock()
	injectReading(t, func() resident.Reading {
		mu.Lock()
		defer mu.Unlock()
		return reading
	})
	ctxZ, cancelZ := context.WithCancel(context.Background())
	zeta := newAdmission(ctxZ, 2)
	if admitted, refusal, _ := zeta.admit(); !admitted {
		t.Fatalf("zeta's package was refused: %q", refusal)
	}
	zeta.spawned("example.com/zeta", 202)
	mu.Lock()
	reading = resident.Reading{
		Set:   resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host:  resident.Memory{TotalBytes: 64 * gib, AvailableBytes: gib / 2},
		Trees: map[int]uint64{202: 3 * gib},
	}
	mu.Unlock()
	cancelZ()
	ctxE, cancelE := context.WithCancel(context.Background())
	defer cancelE()
	eta := newAdmission(ctxE, 8)
	etaAsk := admitAsync(eta)
	select {
	case got := <-etaAsk:
		t.Fatalf("eta beside a cancelled operation's running package was answered %v, want held", got)
	case <-time.After(150 * time.Millisecond):
	}
	mu.Lock()
	reading = resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 4 * gib},
	}
	mu.Unlock()
	zeta.reaped("example.com/zeta", 202, 3*gib)
	zeta.release()
	zeta.leave()
	select {
	case got := <-etaAsk:
		if got[0] != "admitted" {
			t.Fatalf("after the cancelled operation's package was reaped eta was %v, want admitted", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled operation's reap and leave did not admit eta")
	}
	eta.leave()
	// One reading per wake: two waiters woken by one release judge
	// over one reading, taken once — the hook is asked once for the
	// burst, not once per waiter.
	freshGate(t)
	var reads atomic.Int32
	injectReading(t, func() resident.Reading {
		reads.Add(1)
		mu.Lock()
		defer mu.Unlock()
		return reading
	})
	mu.Lock()
	reading = resident.Reading{
		Set:  resident.Set{ProcessBytes: gib / 8, ProcessPeakBytes: gib / 8},
		Host: resident.Memory{TotalBytes: 64 * gib, AvailableBytes: 2 * gib},
	}
	mu.Unlock()
	theta := newAdmission(context.Background(), 8)
	for i := 0; i < 2; i++ {
		if admitted, refusal, _ := theta.admit(); !admitted {
			t.Fatalf("theta's package %d was refused: %q", i, refusal)
		}
	}
	iota, kappa := newAdmission(context.Background(), 8), newAdmission(context.Background(), 8)
	iotaAsk, kappaAsk := admitAsync(iota), admitAsync(kappa)
	time.Sleep(100 * time.Millisecond)
	before := reads.Load()
	theta.release()
	select {
	case <-iotaAsk:
	case <-kappaAsk:
	case <-time.After(5 * time.Second):
		t.Fatal("the release woke neither waiter")
	}
	time.Sleep(100 * time.Millisecond)
	if got := reads.Load() - before; got != 1 {
		t.Fatalf("one release woke two waiters and the host was read %d times, want once for the burst", got)
	}
	theta.leave()
	iota.leave()
	kappa.leave()
}
