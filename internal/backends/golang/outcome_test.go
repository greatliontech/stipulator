package golang

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func outcomeFields(t *testing.T, manifest string) (string, []string) {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Outcome  string   `json:"outcome"`
		Subjects []string `json:"subjects"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m.Outcome, m.Subjects
}

func TestUnsupportedFileOutcomesRetainIdentityWithoutPublishing(t *testing.T) {
	if testing.Short() {
		t.Skip("executes witnesses with unsupported file outcomes")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises")
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := observedReaderModule(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetRace(true)
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("files", cfg)})
	for i := 0; i < 2; i++ {
		report, tr, err := ExecutePolicyWitnessed(context.Background(), mustCapture(t, context.Background(), dir, policy), noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if !SuiteHealthy(report) || tr.Ran != 1 || tr.Uncached != 1 || len(witnesscache.Load(t.Context(), dir).Records) != 0 {
			t.Fatalf("unsupported file read published or lost its outcome: report=%v run=%+v", report, tr)
		}
		obs := report.GetObservations()
		if len(obs) != 1 || obs[0].GetCompleted() == nil {
			t.Fatalf("completed file-reading process lost its identity guards: %v", obs)
		}
		guard := obs[0].GetCompleted()
		paths, err := runtimeinput.ModuleRelPaths(guard.GetManifest())
		if err != nil || !slices.Contains(paths, "data.txt") || guard.GetDigest() == "" {
			t.Fatalf("file identity not retained: paths=%v err=%v guard=%v", paths, err, guard)
		}
		method, subjects := outcomeFields(t, guard.GetManifest())
		if method != "" || len(subjects) != 0 || !strings.Contains(guard.GetOutcomeReason(), "operation-outcome support") {
			t.Fatalf("unsupported observation mislabeled: %v", guard)
		}
	}
	// Explicit purity can license the final verdict, but cannot manufacture
	// support for file outcomes or discard the observed identity's drift guard.
	cfg.SetAssumePure(true)
	_, tr, err := ExecutePolicyWitnessed(context.Background(), mustCapture(t, context.Background(), dir, policy), noSeeding{})
	if err != nil || tr.Uncached != 0 {
		t.Fatalf("explicit purity failed: %+v %v", tr, err)
	}
	records := witnesscache.Load(t.Context(), dir).Records
	if len(records) != 1 || records[0].Fingerprint.PurityAssertion == "" {
		t.Fatalf("missing attributable purity record: %+v", records)
	}
	method, subjects := outcomeFields(t, records[0].Fingerprint.RuntimeInputs)
	if method != "" || len(subjects) != 0 {
		t.Fatal("purity relabeled identity-only evidence as verified outcomes")
	}
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("moved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.Current(context.Background(), records[0].Fingerprint.RuntimeInputs, dir, os.Environ())
	if err != nil || state.Digest == records[0].Fingerprint.RuntimeDigest {
		t.Fatalf("purity lost the file drift guard: %+v %v", state, err)
	}
}

func TestOutcomePreparationMatchesTheActualProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("derives outcome support over a temporary test module")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises")
	neutralAmbient(t)
	dir := observedEnvironmentModule(t)
	ctx := context.Background()
	pkg := "example.com/purefix"
	subject := gofresh.Subject{Package: pkg, Symbol: "TestReadsObservedFixture"}
	env, err := gotool.EnvForCommand(gotool.SetEnv(os.Environ(), "GOWORK", "off"), dir)
	if err != nil {
		t.Fatal(err)
	}
	env, telemetrySource, err := telemetryOffEnvSource(env)
	if err != nil {
		t.Fatal(err)
	}
	session, err := gofresh.NewSession(ctx, gofresh.BuildDescription{Directory: dir, Environment: env, Inputs: []string{}}, gofresh.SessionPolicy{EvidenceRoot: dir, Execution: gofresh.PackageExecution, Runner: engineRunner})
	if err != nil {
		t.Fatal(err)
	}
	view, err := session.Snapshot(ctx, []gofresh.Subject{subject}, gofresh.CodeResult)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := view.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: dir, Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	n := &NormalizedInvocation{Name: "bound", Dir: dir, Env: env, WitnessEnv: env,
		PkgDirs: map[string]string{pkg: dir}, PkgClosureDirs: map[string][]string{pkg: {}}, TelemetrySource: telemetrySource, runnables: map[string][]string{pkg: {subject.Symbol}}}
	query := *n
	query.Packages = []string{pkg}
	roster, err := observeRunnableGeneration(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	leg := &packageLeg{view: &witnessSnapshot{Snapshot: view, roster: roster}, binding: binding}
	goenv, err := rootsRunner.TakeEnvSnapshot(ctx, dir, env)
	if err != nil {
		t.Fatal(err)
	}
	n.GOROOT, n.GOCACHE = goenv.Value("GOROOT"), goenv.Value("GOCACHE")
	built := buildRetained(ctx, n, pkg, retainedProcessHooks{})
	if built.Err != nil {
		t.Fatal(built.Err)
	}
	defer built.Artifact.Close()
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation(n.Name)
	producer.SetProcessOrdinal(1)
	log := filepath.Join(t.TempDir(), "capture.log")
	environment, err := prepareRetainedEnvironment(n, dir, n.GOROOT)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	runtime, err := prepareRetainedRuntime(ctx, n, built.Artifact, retainedRuntimeOptions{Package: pkg, Environment: environment, GOCACHE: n.GOCACHE, Selection: []string{subject.Symbol}, LogPath: log}, retainedProcessHooks{})
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(ctx context.Context, leg *packageLeg, names []string) (*gofresh.PackageUnit, gofresh.Contribution, error) {
		selectedRuntime := *runtime
		selectedRuntime.Args = environment.flags.runtimeArgs(n, names, log)
		return prepareNativeAttempt(ctx, n, pkg, leg, built.Artifact, &selectedRuntime, log, producer)
	}
	unit, contribution, err := prepare(ctx, leg, []string{subject.Symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer unit.Abort()
	if _, _, err := prepare(ctx, nil, []string{subject.Symbol}); err == nil {
		t.Fatal("absent snapshot granted preparation")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := prepare(cancelled, leg, []string{subject.Symbol}); err == nil {
		t.Fatal("cancelled preparation issued support")
	}
	for _, names := range [][]string{{}, {"TestOther"}} {
		if _, _, err := prepare(ctx, leg, names); err == nil {
			t.Fatalf("selection %v borrowed %s's support", names, subject.Symbol)
		}
	}
	mixed, selected, err := prepare(ctx, leg, []string{subject.Symbol, "TestOther"})
	if err != nil {
		t.Fatal(err)
	}
	if got := selected.Subjects(); len(got) != 1 || got[0] != subject {
		t.Fatalf("unmatched spelling became an actual contributor: %v", got)
	}
	mixed.Abort()
	other, _, err := prepare(ctx, leg, []string{subject.Symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Abort()
	if err := other.Submit(contribution, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err == nil {
		t.Fatal("another attempt borrowed the completion handle")
	}
	actual := runRetained(ctx, ctx, n, runtime, producer, retainedProcessHooks{})
	if !actual.HarnessComplete() {
		t.Fatalf("real harness did not complete: %+v", actual)
	}
	if err := unit.Submit(contribution, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
		t.Fatal(err)
	}
	final, err := unit.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	account, _ := unit.Account()
	obs := nativeObservation(pkg, producer, final, account, nil)
	method, subjects := outcomeFields(t, obs.Wire.GetCompleted().GetManifest())
	if method == "" || len(subjects) != 1 || obs.Wire.GetCompleted().GetOutcomeReason() != "" {
		t.Fatalf("actual binding lost support: %v", obs.Wire)
	}
	if _, err := other.Close(ctx); err == nil {
		t.Fatal("unsubmitted sibling borrowed successful close")
	}
	contradicted, handle, err := prepare(ctx, leg, []string{subject.Symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer contradicted.Abort()
	actual = runRetained(ctx, ctx, n, runtime, producer, retainedProcessHooks{})
	if !actual.HarnessComplete() {
		t.Fatal("contradiction fixture did not actually complete")
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("open .git/HEAD\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := contradicted.Submit(handle, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
		t.Fatal(err)
	}
	_, _ = contradicted.Close(ctx)
	contradiction, _ := contradicted.Account()
	if contradiction.Observation != gofresh.IncompleteObservation || !strings.Contains(contradiction.ObservationReason, "captured operations") {
		t.Fatalf("excluded operation hid a contradictory capture: %+v", contradiction)
	}
	leg.release()
	if _, _, err := prepare(ctx, leg, []string{subject.Symbol}); err == nil {
		t.Fatal("released package issued support")
	}
}

func TestOrdinarilyFailedProcessHasIdentityGuardsButNoWitness(t *testing.T) {
	if testing.Short() {
		t.Skip("executes failing and aborting test binaries")
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises")
	neutralAmbient(t)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./mixed", "./panics"})
	cfg.SetPlainWitness(true)
	health, rows, diags, observations := executeInvocationObserved(t, time.Minute, cfg, "failures")
	if health.GetDisposition() == healthy {
		t.Fatalf("failing invocation granted health: %v", diags)
	}
	for _, obs := range observations {
		switch obs.Wire.GetPackage() {
		case "example.com/exec/mixed":
			guard := obs.Wire.GetCompleted()
			if guard == nil {
				t.Fatalf("ordinary failure lost finalized guards: %v", obs.Wire)
			}
			method, subjects := outcomeFields(t, guard.GetManifest())
			if method == "" || len(subjects) != 2 || guard.GetOutcomeReason() != "" || obs.account.Terminal.Kind != gofresh.NormalCompletion {
				t.Fatalf("ordinary failure lost its independently prepared package support: %+v", obs.account)
			}
		case "example.com/exec/panics":
			if obs.Wire.GetCompleted() != nil || obs.Wire.GetIncompleteReason() == "" {
				t.Fatalf("panic granted completion: %v", obs.Wire)
			}
		}
	}
	if len(observations) != 2 {
		t.Fatalf("missing processes: %v", observations)
	}
	report := &stipulatorv1.ExecutionReport{}
	report.SetInvocations([]*stipulatorv1.InvocationHealth{health})
	report.SetTests(rows)
	if _, granted := DeriveTestRun(report).Outcomes["example.com/exec/mixed.TestGreen"]; granted {
		t.Fatal("native outcome support promoted a passing prefix of a failed package into witness evidence")
	}
}
