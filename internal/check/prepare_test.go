package check

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestKnownIDsRefusesUnknownIdentifiers pins the vocabulary refusal
// every id-scoped surface shares: identifiers the corpus does not
// declare refuse by name; declared ones pass.
func TestKnownIDsRefusesUnknownIdentifiers(t *testing.T) {
	stipulate.Covers(t, "REQ-check-preparation")
	prepared, err := Prepare(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := KnownIDs(prepared.Spec, []string{"REQ-a-one"}); err != nil {
		t.Fatalf("declared identifier refused: %v", err)
	}
	err = KnownIDs(prepared.Spec, []string{"REQ-a-one", "REQ-a-two", "REQ-b"})
	if err == nil || !strings.Contains(err.Error(), "REQ-a-two, REQ-b") {
		t.Fatalf("err = %v, want every unknown identifier named", err)
	}
	if len(prepared.Hygiene) != 0 || prepared.Coverage == nil {
		t.Fatalf("prepared = %+v, want clean hygiene and a coverage policy", prepared)
	}
}

// The check result's compile problems carry the compile's remedies as
// their own rows beside the faults — marked as remedies, never counted
// as faults — so an agent reading a broken-corpus verdict sees the
// operation that renders the state (REQ-change-remediation).
func TestCompileProblemsCarryRemedies(t *testing.T) {
	stipulate.Covers(t, "REQ-change-remediation")
	prepared, err := Prepare(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-new** (behavior, supersedes REQ-a-gone): It MUST hold.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	problems := prepared.CompileProblems()
	var fault, remedy bool
	for _, p := range problems {
		switch {
		case strings.HasPrefix(p.GetMessage(), "remedy: if REQ-a-gone was removed by this edit") && strings.Contains(p.GetMessage(), "stipulator dispose supersede --from REQ-a-gone --into REQ-a-new"):
			remedy = true
		case strings.Contains(p.GetMessage(), "supersedes REQ-a-gone, which is neither declared nor tombstoned"):
			fault = true
		}
	}
	if !fault || !remedy || len(problems) != 2 {
		t.Fatalf("compile problems = %v, want the fault and its remedy row", problems)
	}
	// A compiling corpus yields no problems at all — a remedy never
	// stands alone.
	clean, err := Prepare(fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(clean.CompileProblems()) != 0 {
		t.Fatal("a compiling corpus reported problems")
	}
}

type staticResolveBackend struct{}

func (staticResolveBackend) Resolve(symbol string) (verify.Resolution, string, error) {
	return verify.Resolved, "sha256:0000000000000000000000000000000000000000000000000000000000000000", nil
}

func FuzzCheckPreparationRefusesHeldInputFaults(f *testing.F) {
	f.Add(uint8(0), "REQ-a-unknown")
	f.Add(uint8(1), "REQ-b-ghost")
	f.Add(uint8(2), "REQ-a-one")
	f.Add(uint8(3), "REQ-c-dup")
	f.Fuzz(func(t *testing.T, mode uint8, rawID string) {
		stipulate.Covers(t, "REQ-check-preparation")
		switch mode % 4 {
		case 0:
			// Unknown identifiers in KnownIDs refuse in-process.
			prepared, err := Prepare(fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
			})
			if err != nil {
				t.Fatal(err)
			}
			id := strings.TrimSpace(rawID)
			err = KnownIDs(prepared.Spec, []string{id})
			if id == "REQ-a-one" {
				if err != nil {
					t.Fatalf("known ID refused: %v", err)
				}
			} else if err == nil {
				t.Fatalf("unknown ID %q accepted", id)
			}
		case 1:
			// Unresolved spec reference surfaces as a compile problem in Prepare without spawning children.
			prepared, err := Prepare(fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                     {Data: []byte("# A\n\n**REQ-a-one** (behavior): Per REQ-z-missing it MUST hold.\n")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(prepared.CompileProblems()) == 0 {
				t.Fatal("unresolved reference produced no compile problems")
			}
		case 2:
			// Duplicate coverage cell in manifest refuses at Prepare.
			_, err := Prepare(fstest.MapFS{
				".stipulator/manifest.textproto": {Data: []byte(
					"include: \"specs/**/*.md\"\n" +
						"policy { kind: CLAUSE_KIND_BEHAVIOR keyword: KEYWORD_MUST minimum: MINIMUM_EVIDENCE_WITNESS }\n" +
						"policy { kind: CLAUSE_KIND_BEHAVIOR keyword: KEYWORD_MUST minimum: MINIMUM_EVIDENCE_STATIC }\n",
				)},
				"specs/a.md": {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
			})
			if err == nil || !strings.Contains(err.Error(), "twice") {
				t.Fatalf("duplicate coverage cell accepted: %v", err)
			}
		case 3:
			// Dangling binding requirement surfaces in Prepared.Hygiene before any witness run.
			prepared, err := Prepare(fstest.MapFS{
				".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
				"specs/a.md":                       {Data: []byte("# A\n\n**REQ-a-one** (behavior): It MUST hold.\n")},
				".stipulator/bindings/a.textproto": {Data: []byte("bindings {\n  requirement_id: \"REQ-a-dangling\"\n  backend: \"go\"\n  symbol: \"example.com/p.TestA\"\n  role: BINDING_ROLE_TESTS\n}\n")},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(prepared.Hygiene) == 0 {
				t.Fatal("dangling binding requirement produced no hygiene problem")
			}
		}
	})
}

func FuzzLadderExecutesRunTestsAtMostOnce(f *testing.F) {
	f.Add(true, uint8(1))
	f.Add(false, uint8(3))
	f.Add(true, uint8(4))
	f.Fuzz(func(t *testing.T, witnessed bool, countRaw uint8) {
		stipulate.Covers(t, "REQ-core-one-execution")
		n := int(countRaw%5) + 1
		req := &stipulatorv1.Requirement{}
		req.SetId("REQ-a-one")
		req.SetKind(stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR)
		req.SetKeyword(stipulatorv1.Keyword_KEYWORD_MUST)
		req.SetContentHash("h1")
		req.SetSourceHash("s1")
		spec := &stipulatorv1.Spec{}
		spec.SetRequirements([]*stipulatorv1.Requirement{req})

		var bindings []*stipulatorv1.Binding
		outcomes := map[string]verify.TestOutcome{}
		for i := range n {
			sym := fmt.Sprintf("example.com/p.Test%d", i)
			b := &stipulatorv1.Binding{}
			b.SetRequirementId("REQ-a-one")
			b.SetContentHash("h1")
			b.SetSourceHash("s1")
			b.SetBackend("go")
			b.SetSymbol(sym)
			b.SetRole(stipulatorv1.BindingRole_BINDING_ROLE_TESTS)
			b.SetShapeHash("sha256:0000000000000000000000000000000000000000000000000000000000000000")
			bindings = append(bindings, b)
			outcomes[sym] = verify.TestPassed
		}
		set := &stipulatorv1.BindingSet{}
		set.SetBindings(bindings)
		store := &records.Store{
			Bindings: []records.BindingFile{{Path: ".stipulator/bindings/a.textproto", Set: set}},
		}

		runs := 0
		var runFn func(context.Context, verify.WitnessSeeding) (*verify.TestRun, error)
		if witnessed {
			runFn = func(context.Context, verify.WitnessSeeding) (*verify.TestRun, error) {
				runs++
				return &verify.TestRun{Outcomes: outcomes, Fresh: n}, nil
			}
		}
		vr, tr, err := Ladder(context.Background(), spec, store, nil, witnessed, LadderDeps{
			Backends: func(context.Context, []string) (map[string]verify.Backend, error) {
				return map[string]verify.Backend{"go": staticResolveBackend{}}, nil
			},
			RunTests: runFn,
		})
		if err != nil {
			t.Fatal(err)
		}
		wantRuns := 0
		if witnessed {
			wantRuns = 1
		}
		if runs != wantRuns {
			t.Fatalf("RunTests invoked %d times, want %d", runs, wantRuns)
		}
		if witnessed && (tr == nil || vr.TestsPassed != n) {
			t.Fatalf("witnessed correlation = %+v, want %d passed from single execution", vr, n)
		}
		if !witnessed && (tr != nil || vr.Witnessed) {
			t.Fatalf("unwitnessed ladder produced test run %+v", tr)
		}
	})
}
