package golang

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

// nativeCauseTestError supplies the native structured-cause protocol for injected
// closing refusals. It carries no finalized subjects or reusable evidence.
type nativeCauseTestError struct{ cause gofresh.Cause }

func (e *nativeCauseTestError) Error() string {
	return "gofresh: runtime-input producing span changed: observation bracket moved: reader"
}
func (e *nativeCauseTestError) Cause() gofresh.Cause { return e.cause }

type nativeCauseTestWrapper struct {
	*nativeCauseTestError
	inner error
}

func (e *nativeCauseTestWrapper) Unwrap() error { return e.inner }

func TestRuntimeInputMovementClassifiesOnlyGuardChanges(t *testing.T) {
	motion := &nativeCauseTestError{gofresh.Cause{Kind: gofresh.RuntimeInputsCause, Condition: gofresh.Changed}}
	config := &nativeCauseTestError{gofresh.Cause{Kind: gofresh.BuildConfigCause, Condition: gofresh.Changed}}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed", motion, true},
		{"wrapped", fmt.Errorf("closing: %w", motion), true},
		{"nested-wrap", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", motion)), true},
		{"join-only-motion", errors.Join(motion, fmt.Errorf("other guard: %w", motion)), true},
		{"same-message", errors.New(motion.Error()), false},
		{"source", gofresh.ErrViewChanged, false},
		{"config", config, false},
		{"unavailable", &nativeCauseTestError{gofresh.Cause{Kind: gofresh.RuntimeInputsCause, Condition: gofresh.Unavailable}}, false},
		{"cancel", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"joined-fault", errors.Join(motion, errors.New("disk failure")), false},
		{"fault-first", errors.Join(errors.New("disk failure"), motion), false},
		{"nested-joined-fault", fmt.Errorf("closing: %w", errors.Join(motion, errors.Join(motion, config))), false},
		{"joined-source", errors.Join(motion, gofresh.ErrViewChanged), false},
		{"joined-cancel", errors.Join(motion, context.Canceled), false},
		{"wrapped-deadline", fmt.Errorf("closing: %w", errors.Join(motion, context.DeadlineExceeded)), false},
		{"conflicting-wrapper", &nativeCauseTestWrapper{config, motion}, false},
		{"motion-wrapping-fault", &nativeCauseTestWrapper{motion, errors.New("disk failure")}, false},
		{"motion-wrapping-cancel", &nativeCauseTestWrapper{motion, context.Canceled}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeInputMovement(tc.err); got != tc.want {
				t.Fatalf("classification=%t want %t: %v", got, tc.want, tc.err)
			}
			if tc.want {
				var actual *nativeCauseTestError
				if !errors.As(tc.err, &actual) || actual != motion {
					t.Fatal("wrapped cause identity lost")
				}
			}
		})
	}
}

func movementPublicationFixture(err error) (*captureGroup, *packageLeg, packageUnit) {
	pkg := "example.test/p"
	names := []string{"TestA", "TestB"}
	leg := &packageLeg{view: &witnessSnapshot{}}
	g := &captureGroup{invs: []string{"inv"}, packages: map[string]*groupPackage{pkg: {inv: "inv", names: names}}, legs: map[string]*packageLeg{pkg: leg}, view: leg.view}
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation("inv")
	producer.SetProcessId(101)
	producer.SetProcessOrdinal(1)
	var rows []*stipulatorv1.TestResult
	for _, name := range []string{"TestA", "TestA/sub", "TestB"} {
		row := &stipulatorv1.TestResult{}
		row.SetPackage(pkg)
		row.SetTest(name)
		row.SetOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED)
		row.SetProducer(producer)
		rows = append(rows, row)
	}
	account := gofresh.ProcessAccount{Process: "attempt", Prepared: true, Terminal: gofresh.Terminal{Kind: gofresh.NormalCompletion}}
	obs := nativeObservation(pkg, producer, gofresh.FinalizedProcess{}, account, err)
	return g, leg, packageUnit{pkg: pkg, run: packageRun{pkg: pkg, producer: producer, tests: rows, obs: obs, disposition: stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY}}
}

func TestProducingCloseRefusalScope(t *testing.T) {
	motion := &nativeCauseTestError{gofresh.Cause{Kind: gofresh.RuntimeInputsCause, Condition: gofresh.Changed}}
	for _, tc := range []struct {
		name                  string
		err                   error
		local, cancel, config bool
	}{
		{"typed-motion", motion, true, false, false},
		{"wrapped-motion", fmt.Errorf("close: %w", motion), true, false, false},
		{"same-message-fault", errors.New(motion.Error()), false, false, false},
		{"source-view", gofresh.ErrViewChanged, false, false, false},
		{"configuration", &nativeCauseTestError{gofresh.Cause{Kind: gofresh.BuildConfigCause, Condition: gofresh.Changed}}, false, false, false},
		{"mixed-fault", errors.Join(motion, errors.New("infrastructure failure")), false, false, false},
		{"closing-cancellation", context.Canceled, false, false, false},
		{"caller-cancellation", motion, false, true, false},
		{"later-config-validation", motion, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, leg, unit := movementPublicationFixture(tc.err)
			defer leg.dispose()
			account := unit.run.obs.account
			if tc.config {
				leg.inputErr = errors.New("configuration validation failed")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			r := &WitnessRecorder{}
			records, reasons, degraded, err := r.publishPackage(ctx, g, unit.pkg, leg, producerCandidate{healthy: true, obs: unit.run.obs, rows: unit.run.tests}, nil)
			if len(records) != 0 {
				t.Fatal("refused process gained reusable records")
			}
			if tc.cancel {
				if !errors.Is(err, context.Canceled) || degraded != "" || len(reasons) != 0 {
					t.Fatalf("cancellation lost precedence: %v %q %v", err, degraded, reasons)
				}
			} else {
				if err != nil || (degraded == "") != tc.local {
					t.Fatalf("publication refusal scope: err=%v degraded=%q local=%t", err, degraded, tc.local)
				}
				if leg.view != nil || g.view != nil {
					t.Fatal("completion did not release its view")
				}
				if tc.local {
					if len(reasons) != 2 {
						t.Fatalf("reasons=%v, want both top-level siblings, not the subtest", reasons)
					}
					for _, name := range g.packages[unit.pkg].names {
						why := reasons[gofresh.Subject{Package: unit.pkg, Symbol: name}]
						if why.class != reasonPostRun || !strings.Contains(why.detail, tc.err.Error()) {
							t.Fatalf("sibling %s reason=%v", name, why)
						}
					}
				}
				if tc.config && !strings.Contains(degraded, "configuration validation failed") {
					t.Fatal("movement hid configuration validation", degraded)
				}
			}
			if unit.run.obs.closeErr != tc.err || !reflect.DeepEqual(unit.run.obs.account, account) || len(unit.run.obs.final.Subjects) != 0 {
				t.Fatal("closing refusal or non-authorizing account was rewritten")
			}
			for _, row := range unit.run.tests {
				if row.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
					t.Fatal("execution outcome rewritten")
				}
			}
		})
	}
}

func TestSelectiveMovementRefusesEveryAttemptSibling(t *testing.T) {
	motion := &nativeCauseTestError{gofresh.Cause{Kind: gofresh.RuntimeInputsCause, Condition: gofresh.Changed}}
	for _, tc := range []struct {
		name         string
		err          error
		drift, local bool
	}{
		{"movement", fmt.Errorf("close: %w", motion), true, true},
		{"infrastructure", errors.New(motion.Error()), false, false},
		{"mixed", errors.Join(motion, errors.New("configuration failure")), true, false},
		{"mixed-cancellation", errors.Join(motion, context.Canceled), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, leg, unit := movementPublicationFixture(tc.err)
			defer leg.dispose()
			wg := &witnessGroup{g: g, legs: g.legs, stale: map[string][]string{unit.pkg: {"TestA", "TestB"}}}
			// A second refused attempt for A must not repair the first process
			// or erase B's refusal. Subtest rows are not additional candidates.
			_, soloLeg, solo := movementPublicationFixture(tc.err)
			defer soloLeg.dispose()
			solo.run.producer.SetProcessId(102)
			solo.run.producer.SetProcessOrdinal(2)
			solo.run.obs.account.Process = "solo-attempt"
			solo.run.tests = solo.run.tests[:1]
			solo.run.soloTest = "TestA"
			unit.solos = []packageRun{solo.run}
			merge := newExecMerge()
			merge.addUnit("inv", unit)
			records, reasons, _, err := publishExecuted(t.Context(), wg, unit.pkg, merge)
			if err != nil || len(records) != 0 || len(reasons) != 2 {
				t.Fatalf("selective publication: %v %v %v", records, reasons, err)
			}
			want := reasonSourceFailed
			if tc.local {
				want = reasonPostRun
			}
			for _, why := range reasons {
				if why.class != want {
					t.Fatalf("reason=%v want class=%v", why, want)
				}
			}
			retryReasons := map[gofresh.Subject]uncacheable{}
			candidates := collectRetryCandidates(wg, unit, merge, retryReasons)
			wantDenied := 0
			if tc.drift {
				wantDenied = 3
			}
			if len(candidates) != 3 || len(merge.denied) != wantDenied {
				t.Fatalf("attempt roster: candidates=%d denied=%d want 3/%d", len(candidates), len(merge.denied), wantDenied)
			}
			wantReasons := 0
			if tc.drift {
				wantReasons = 2
			}
			if len(retryReasons) != wantReasons {
				t.Fatalf("retry reasons=%v want %d subjects", retryReasons, wantReasons)
			}
			for _, why := range retryReasons {
				if why.class != want {
					t.Fatalf("retry lost fault/movement classification: %v", why)
				}
			}
			for _, c := range candidates {
				if c.native != nil {
					t.Fatal("closing refusal gained native evidence")
				}
			}
			if len(merge.rows) != 4 || len(merge.obs) != 2 || len(merge.disp) != 2 {
				t.Fatal("raw attempts were merged or dropped")
			}
			for _, row := range merge.rows {
				if row.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED || merge.disp[keyOfProducer(row.GetProducer())] != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
					t.Fatal("raw outcomes/health changed")
				}
			}
			for _, obs := range merge.obs {
				if obs.closeErr != tc.err || obs.account.Terminal.Kind != gofresh.NormalCompletion {
					t.Fatal("attempt account changed")
				}
			}
		})
	}
}

func TestRuntimeMovementAllowsLaterPackagePublication(t *testing.T) {
	if testing.Short() {
		t.Skip("executes two real retained package processes")
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":          "module example.com/localmotion\n\ngo 1.26\n",
		"first/p_test.go": "package first\nimport \"testing\"\n//gofresh:pure\nfunc TestA(t *testing.T){}\n//gofresh:pure\nfunc TestB(t *testing.T){}\n",
		"later/p_test.go": "package later\nimport \"testing\"\n//gofresh:pure\nfunc TestLater(t *testing.T){}\n",
	})
	first, later := &stipulatorv1.GoInvocationConfig{}, &stipulatorv1.GoInvocationConfig{}
	first.SetRace(true)
	first.SetPackages([]string{"./first"})
	later.SetRace(true)
	later.SetPackages([]string{"./later"})
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("a-first", first), goInvocation("b-later", later)})
	motion := &nativeCauseTestError{gofresh.Cause{Kind: gofresh.RuntimeInputsCause, Condition: gofresh.Changed}}
	priorClose, priorPublish := closeNativeProcess, beforePackagePublishForTest
	t.Cleanup(func() { closeNativeProcess, beforePackagePublishForTest = priorClose, priorPublish })
	var order []string
	refuseNext := false
	beforePackagePublishForTest = func(pkg string) { order = append(order, pkg); refuseNext = pkg == "example.com/localmotion/first" }
	var account gofresh.ProcessAccount
	closeNativeProcess = func(ctx context.Context, u *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
		if refuseNext {
			refuseNext = false
			account, _ = u.Account()
			return gofresh.FinalizedProcess{}, fmt.Errorf("native closing: %w", motion)
		}
		return priorClose(ctx, u)
	}
	report, run, err := ExecutePolicyWitnessed(t.Context(), mustCapture(t, t.Context(), dir, policy), noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Degraded != "" || !SuiteHealthy(report) || run.Ran != 3 || run.Uncached != 2 {
		t.Fatalf("local refusal became whole-run fault: %+v error=%v", run, err)
	}
	if !reflect.DeepEqual(order, []string{"example.com/localmotion/first", "example.com/localmotion/later"}) {
		t.Fatal("later control did not close after the refusal", order)
	}
	if !account.Prepared || account.Terminal.Kind != gofresh.NormalCompletion || account.Process == "" {
		t.Fatal("refused attempt lacked real execution account", account)
	}
	cache := witnesscache.Load(t.Context(), dir).Records
	for _, name := range []string{"TestA", "TestB"} {
		key := "example.com/localmotion/first." + name
		if run.Outcomes[key] != verify.TestPassed || !strings.HasPrefix(run.UncacheableReasons[key], "post-run validation: ") || cacheRecord(t, cache, "example.com/localmotion/first", name) != nil {
			t.Fatalf("refused sibling %s changed outcome or retained evidence: %+v", name, run)
		}
	}
	if run.Outcomes["example.com/localmotion/later.TestLater"] != verify.TestPassed || cacheRecord(t, cache, "example.com/localmotion/later", "TestLater") == nil {
		t.Fatal("later independent package failed to publish")
	}
	if len(report.GetObservations()) != 2 {
		t.Fatal("process accounts dropped", report.GetObservations())
	}
	for _, obs := range report.GetObservations() {
		if obs.GetProducer().GetProcessId() <= 0 {
			t.Fatal("observation lost its producer")
		}
		if obs.GetPackage() == "example.com/localmotion/first" && !strings.Contains(obs.GetIncompleteReason(), motion.Error()) {
			t.Fatal("closing refusal account lost", obs)
		}
	}
}
