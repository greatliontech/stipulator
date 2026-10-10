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
		if !SuiteHealthy(report) || tr.Ran != 1 || tr.Uncached != 1 || len(witnesscache.Load(t.Context(), dir)) != 0 {
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
	records := witnesscache.Load(t.Context(), dir)
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
	engine, err := gofresh.New(gofresh.WithDir(dir), gofresh.WithEnv(env...))
	if err != nil {
		t.Fatal(err)
	}
	view, err := engine.NewView(ctx, []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	leg, err := newPackageLeg(view, []gofresh.Subject{subject})
	if err != nil {
		t.Fatal(err)
	}
	leg.prove(ctx, []gofresh.Subject{subject})
	n := &NormalizedInvocation{Name: "bound", Dir: dir, Env: env, WitnessEnv: env,
		PkgDirs: map[string]string{pkg: dir}, PkgClosureDirs: map[string][]string{pkg: {}}, roots: new(runtimeinput.Roots)}
	frame := captureObservationFrame(ctx, n, pkg)
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation(n.Name)
	producer.SetProcessOrdinal(1)
	identity := processIdentity(n, producer, pkg)
	frame.outcome = leg.prepareOutcome(ctx, []string{subject.Symbol}, frame.frame, identity)
	binding, err := frame.frame.OutcomeBinding(identity, env)
	if err != nil || frame.outcome.Reason(binding) != "" {
		t.Fatalf("supported fixture refused: %s %v", frame.outcome.Reason(binding), err)
	}
	if support := (*packageLeg)(nil).prepareOutcome(ctx, nil, frame.frame, identity); support.Reason(binding) == "" {
		t.Fatal("a process without a proof leg gained outcome support")
	}
	if support := leg.prepareOutcome(ctx, nil, frame.frame, identity); support.Reason(binding) != "" {
		t.Fatalf("whole-package solo prediction lost its support: %s", support.Reason(binding))
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if support := leg.prepareOutcome(cancelled, nil, frame.frame, identity); support.Reason(binding) == "" {
		t.Fatal("cancelled preparation issued support")
	}
	for _, names := range [][]string{{}, {"TestOther"}, {subject.Symbol, "TestOther"}} {
		if support := leg.prepareOutcome(ctx, names, frame.frame, identity); support.Reason(binding) == "" {
			t.Fatalf("selection %v borrowed %s's support", names, subject.Symbol)
		}
	}
	log := filepath.Join(t.TempDir(), "capture.log")
	if err := os.WriteFile(log, []byte("# test log\ngetenv STIPULATOR_OBSERVED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &streamState{terminal: "pass", started: map[string]bool{}}
	ingest := func(n *NormalizedInvocation, frame observationFrame) *ProcessObservation {
		return observeProcess(ctx, n, pkg, producer, st, nil, healthy, log, frame)
	}
	obs := ingest(n, frame)
	method, subjects := outcomeFields(t, obs.Wire.GetCompleted().GetManifest())
	if method == "" || len(subjects) != 1 || obs.Wire.GetCompleted().GetOutcomeReason() != "" {
		t.Fatalf("actual binding lost its support: %v", obs.Wire)
	}
	// An independently captured span cannot borrow a capability, even with the
	// same paths. Its normally completed identity capture remains useful.
	otherFrame := captureObservationFrame(ctx, n, pkg)
	otherFrame.outcome = frame.outcome
	changedEnv := *n
	changedEnv.WitnessEnv = gotool.SetEnv(env, "STIPULATOR_OBSERVED", "different")
	for _, other := range []*ProcessObservation{ingest(n, otherFrame), ingest(&changedEnv, frame)} {
		guard := other.Wire.GetCompleted()
		if guard == nil {
			t.Fatalf("identity-only fallback lost: %v", other.Wire)
		}
		method, subjects := outcomeFields(t, guard.GetManifest())
		if method != "" || len(subjects) != 0 || guard.GetOutcomeReason() == "" {
			t.Fatalf("support crossed an execution binding: %v", other.Wire)
		}
	}
	if err := os.WriteFile(log, []byte("# test log\nopen .git/HEAD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if contradicted := ingest(n, frame); contradicted.Wire.GetCompleted() != nil || !strings.Contains(contradicted.Wire.GetIncompleteReason(), "captured operations") {
		t.Fatalf("excluded file operation hid a contradictory capture: %v", contradicted.Wire)
	}
	leg.release()
	if support := leg.prepareOutcome(ctx, nil, frame.frame, identity); support.Reason(binding) == "" {
		t.Fatal("a released package still issued outcome support")
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
	health, _, diags, observations := executeInvocationObserved(t, time.Minute, cfg, "failures")
	if health.GetDisposition() == healthy {
		t.Fatalf("failing invocation granted health: %v", diags)
	}
	for _, obs := range observations {
		switch obs.Wire.GetPackage() {
		case "example.com/exec/mixed":
			guard := obs.Wire.GetCompleted()
			if guard == nil || guard.GetOutcomeReason() == "" {
				t.Fatalf("ordinary failure lost finalized guards: %v", obs.Wire)
			}
			method, subjects := outcomeFields(t, guard.GetManifest())
			if method != "" || len(subjects) != 0 {
				t.Fatal("failing process health supplied outcome support")
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
}
