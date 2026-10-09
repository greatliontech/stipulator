package verify

import (
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
)

// countingBackend answers every question and counts the class ones.
type countingBackend struct{ classes int }

func (b *countingBackend) Resolve(string) (Resolution, string, error) {
	return Resolved, strings.Repeat("a", 64), nil
}

func (b *countingBackend) WitnessClassVerdict(string) (WitnessClass, string) {
	b.classes++
	return PropertyWitness, "generator-driven"
}

// Run is Correlate over Resolve: the half that asks the backends asks
// everything before any execution — the class included exactly when the
// pass will witness — and the half that reads the run touches no
// backend, so a pass can release its backends between the two
// (REQ-evidence-resolution-freshness-quiesce).
func TestRunIsCorrelateOverResolve(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-quiesce")
	id := "REQ-x"
	req := &stipulatorv1.Requirement{}
	req.SetId(id)
	spec := &stipulatorv1.Spec{}
	spec.SetRequirements([]*stipulatorv1.Requirement{req})
	b := &stipulatorv1.Binding{}
	b.SetRequirementId(id)
	b.SetBackend("go")
	b.SetSymbol("example.com/p.TestA")
	b.SetRole(stipulatorv1.BindingRole_BINDING_ROLE_TESTS)
	// A second witness whose run failed: rigor qualifies a passing
	// outcome alone, so its row never claims the run's race tier.
	failed := &stipulatorv1.Binding{}
	failed.SetRequirementId(id)
	failed.SetBackend("go")
	failed.SetSymbol("example.com/p.TestB")
	failed.SetRole(stipulatorv1.BindingRole_BINDING_ROLE_TESTS)
	set := &stipulatorv1.BindingSet{}
	set.SetBindings([]*stipulatorv1.Binding{b, failed})
	store := &records.Store{Bindings: []records.BindingFile{{Path: ".stipulator/bindings/a.textproto", Set: set}}}
	run := &TestRun{RaceEnabled: true, Outcomes: map[string]TestOutcome{"example.com/p.TestA": TestPassed, "example.com/p.TestB": TestFailed},
		Registrations: []Registration{{Package: "example.com/p", Test: "TestA", Requirement: id}}}

	// The witnessing resolution asks each witness's class once; the
	// correlation asks the backend nothing more.
	backend := &countingBackend{}
	resolved := Resolve(spec, store, map[string]Backend{"go": backend}, true)
	if backend.classes != 2 || resolved.Witnessed || len(resolved.Results) != 2 || resolved.Results[0].WitnessClass != PropertyWitness {
		t.Fatalf("resolve asked %d classes, witnessed %v, rows %+v", backend.classes, resolved.Witnessed, resolved.Results)
	}
	correlated := Correlate(resolved, store, run)
	passed, red := correlated.Results[0], correlated.Results[1]
	if backend.classes != 2 || !correlated.Witnessed || passed.TestOutcome != TestPassed || !passed.RaceEnabled || len(correlated.Registrations) != 1 || correlated.TestsPassed != 1 {
		t.Fatalf("correlate asked the backend again or read the run wrong: classes %d, %+v", backend.classes, correlated)
	}
	if red.TestOutcome != TestFailed || red.RaceEnabled || correlated.TestsFailed != 1 {
		t.Fatalf("the failed witness reads %+v (failed %d); want its failure with no race tier claimed", red, correlated.TestsFailed)
	}
	// The no-test form asks no class; a nil run leaves the report
	// unwitnessed with the rows' outcomes unset.
	quiet := &countingBackend{}
	if rep := Correlate(Resolve(spec, store, map[string]Backend{"go": quiet}, false), store, nil); quiet.classes != 0 || rep.Witnessed || rep.Results[0].TestOutcome != TestNotRun {
		t.Fatalf("the no-test form asked %d classes or read as witnessed: %+v", quiet.classes, rep)
	}
	// Run is the composition, row for row.
	whole := Run(spec, store, map[string]Backend{"go": &countingBackend{}}, run)
	if whole.Results[0] != passed || whole.Results[1] != red || whole.TestsPassed != correlated.TestsPassed || whole.TestsFailed != correlated.TestsFailed || len(whole.Registrations) != len(correlated.Registrations) || whole.Witnessed != correlated.Witnessed {
		t.Fatalf("Run = %+v; Correlate(Resolve) = %+v", whole, correlated)
	}
}
