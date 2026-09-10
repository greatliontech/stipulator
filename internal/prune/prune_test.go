package prune

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/greatliontech/gofresh"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The resolved-record evaluation is pinned to the serving class: the
// core's source has the guard call after the scoped-pass call in
// source order, so a wrong witness source is a loud refusal on both
// faces at once (REQ-gap-resolved-pruned). (A structural calls-verb
// prover would subsume this pin; that capability is an open design
// question.)
func TestPruneCallSitePinsServingClassRefusal(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "prune.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	sawRun, sawGuardAfter := false, false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Scoped" {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "verifyrun" {
				sawRun = true
			}
		}
		if sel.Sel.Name == "ServingClassRequired" {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "verify" && sawRun {
				sawGuardAfter = true
			}
		}
		return true
	})
	if !sawRun || !sawGuardAfter {
		t.Fatalf("prune.go call-site pin: Scoped=%v guard-after=%v - the serving-class guard must follow the scoped pass", sawRun, sawGuardAfter)
	}
}

// A face's dependencies that refuse every leg the mode must not reach.
func untouchable(t *testing.T, prepared *check.Prepared) Deps {
	t.Helper()
	return Deps{
		Root: t.TempDir(),
		Deps: verbcore.Deps{
			Prepare: func() (*check.Prepared, error) { return prepared, nil },
			Capture: func(context.Context) (*golang.Capture, error) {
				t.Fatal("the policy was captured")
				return nil, nil
			},
			Backends: func(context.Context, []string) (map[string]verify.Backend, error) {
				t.Fatal("backends were built")
				return nil, nil
			},
			RunTests: func(context.Context, *golang.Capture, verify.WitnessSeeding, map[gofresh.Subject]bool, string) (*verify.TestRun, error) {
				t.Fatal("a witness run started")
				return nil, nil
			},
		},
		Compile: func() (*stipulatorv1.Spec, error) { return prepared.Spec, nil },
		Load:    func() (*records.Store, error) { return prepared.Store, nil },
	}
}

// No gap records means nothing can resolve, so the evaluation gathers no
// witness evidence at all: no capture, no backends, no run
// (REQ-gap-resolved-pruned).
func TestEvaluateGathersNothingWithoutGapRecords(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned")
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: &records.Store{}}
	out, err := Evaluate(context.Background(), untouchable(t, prepared), false)
	if err != nil || out.Evaluated || out.Gaps != 0 || len(out.Prunes) != 0 {
		t.Fatalf("gapless evaluation = %+v, %v; want the deletion-only fast path", out, err)
	}
}

// The dangling repair is a corpus-and-records judgment: no witnesses, no
// symbol resolution, and no verification gate — a dangling gap IS a
// verification problem, so gating its repair on clean verification
// would deadlock it (REQ-gap-prune-dangling).
func TestDanglingJudgesFromTheCorpusAlone(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-prune-dangling")
	ghost := "REQ-ghost"
	store := &records.Store{Gaps: []records.GapFile{{Path: ".stipulator/gaps/ghost.textproto", Gap: stipulatorv1.Gap_builder{RequirementId: &ghost}.Build()}}}
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: store, Hygiene: []verify.Problem{{}}}
	deps := untouchable(t, prepared)
	// The repair compiles and loads; the preparation's other refusals
	// (a policy cell named twice, a hygiene problem) never gate it.
	deps.Prepare = func() (*check.Prepared, error) {
		t.Fatal("the repair went through the preparation")
		return nil, nil
	}
	prunes, err := Dangling(deps)
	if err != nil || len(prunes) != 1 || prunes[0].Path != store.Gaps[0].Path || prunes[0].Content != nil {
		t.Fatalf("dangling repair = %+v, %v; want the ghost's deletion despite the hygiene problem", prunes, err)
	}
}

// The store mode composes with nothing else.
func TestModeRefusesStoreCompositions(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	for _, m := range []Mode{{Store: true, Check: true}, {Store: true, Dangling: true}, {Store: true, NoTest: true}} {
		if err := m.Validate(); err == nil {
			t.Fatalf("%+v accepted", m)
		}
	}
	if err := (Mode{Store: true}).Validate(); err != nil {
		t.Fatal(err)
	}
}

// A hygiene problem — the record-only half of verification — refuses
// the evaluation before any child process: no capture, no backends, no
// run (REQ-check-preparation), as one typed refusal a face renders
// (REQ-gap-resolved-pruned).
func TestEvaluateRefusesHygieneBeforeAnyChild(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-resolved-pruned", "REQ-check-preparation")
	id := "REQ-x"
	store := &records.Store{Gaps: []records.GapFile{{Path: ".stipulator/gaps/x.textproto", Gap: stipulatorv1.Gap_builder{RequirementId: &id}.Build()}}}
	prepared := &check.Prepared{Spec: &stipulatorv1.Spec{}, Store: store, Hygiene: []verify.Problem{{Path: "a", Message: "stale"}}}
	_, err := Evaluate(context.Background(), untouchable(t, prepared), false)
	var pe *ProblemsError
	if !errors.As(err, &pe) || len(pe.Problems) != 1 || pe.Problems[0].Message != "stale" {
		t.Fatalf("hygiene refusal = %v, want the one typed problem", err)
	}
}
