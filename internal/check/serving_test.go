package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/stipulate"
)

// writeFileUnder rewrites one file inside an existing fixture tree.
func writeFileUnder(dir, path, content string) error {
	return os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(content), 0o644)
}

// A default check on a warm tree serves proven-fresh witnesses instead of
// executing, claims no suite health, and still renders a full verdict —
// the witness-evidence class of REQ-check-verdict.
func TestCheckServesFreshWitnessesByDefault(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	files := baseTree(nil)
	files[".stipulator/policy.textproto"] = racePolicy
	dir := writeTree(t, files)

	warm, err := Run(context.Background(), dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !warm.GetPassed() || !warm.GetSuiteHealthJudged() || warm.GetExecution() == nil {
		t.Fatalf("full check = passed=%t judged=%t execution=%t, want a passing health-judged run",
			warm.GetPassed(), warm.GetSuiteHealthJudged(), warm.GetExecution() != nil)
	}
	if warm.GetTestsExecuted() == 0 {
		t.Fatal("full check executed nothing; the fixture no longer warms the cache")
	}

	served, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if served.GetSuiteHealthJudged() || served.GetExecution() != nil {
		t.Fatalf("default check claimed suite health (judged=%t execution=%t)",
			served.GetSuiteHealthJudged(), served.GetExecution() != nil)
	}
	if served.GetTestsServed() == 0 || served.GetTestsExecuted() != 0 {
		t.Fatalf("default check served=%d executed=%d, want everything served on an unchanged tree",
			served.GetTestsServed(), served.GetTestsExecuted())
	}
	if !served.GetPassed() {
		t.Error("default check failed on the warm passing tree")
	}
}

// A source edit stales its witness: the default check re-executes exactly
// the stale remainder instead of the whole policy.
func TestCheckDefaultExecutesOnlyTheStaleRemainder(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	files := baseTree(nil)
	files[".stipulator/policy.textproto"] = racePolicy
	dir := writeTree(t, files)
	if _, err := Run(context.Background(), dir, true, nil); err != nil {
		t.Fatal(err)
	}

	// Behavior-preserving byte change: the closure hash moves, the test
	// stays green.
	if err := writeFileUnder(dir, "ok/ok.go", fixtureOK+"\n// moved\n"); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetTestsExecuted() == 0 {
		t.Fatal("stale witness was not re-executed")
	}
	if res.GetSuiteHealthJudged() {
		t.Error("selective re-execution claimed suite health")
	}
	if !res.GetPassed() {
		t.Error("default check failed although the edit preserved behavior")
	}
}

// A red witness surfaced by the default check fails the verdict through
// its bound requirement and carries its retained output on the result —
// no execution report exists to carry it (REQ-check-diagnostics).
func TestCheckDefaultRedWitnessFailsWithDiagnostics(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-check-diagnostics")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	files := baseTree(map[string]string{
		"specs/check.md": "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		".stipulator/bindings/tests.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-must\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	})
	files[".stipulator/policy.textproto"] = racePolicy
	dir := writeTree(t, files)
	if _, err := Run(context.Background(), dir, true, nil); err != nil {
		t.Fatal(err)
	}

	if err := writeFileUnder(dir, "ok/ok.go", "package ok\n\nfunc Double(x int) int { return 3 * x }\n"); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("default check passed despite a red bound witness")
	}
	var failed *stipulatorv1.FailureDiagnostic
	for _, d := range res.GetWitnessDiagnostics() {
		if d.GetPackage() == "example.com/checkfix/ok" && d.GetTest() == "TestDouble" {
			failed = d
		}
	}
	if failed == nil {
		t.Fatalf("witness diagnostics = %v, want the red test's typed row", res.GetWitnessDiagnostics())
	}
	if failed.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
		t.Errorf("disposition = %v, want an assertion failure named distinctly from degradation", failed.GetDisposition())
	}
	if !strings.Contains(failed.GetOutput(), "broken arithmetic") {
		t.Errorf("retained output %q does not carry the failure text", failed.GetOutput())
	}
	// One fact, one home per payload: the rows ride the check level, so
	// the verify sub-message carries none.
	if dup := res.GetVerify().GetWitnessDiagnostics(); len(dup) != 0 {
		t.Errorf("verify sub-message duplicates %d diagnostic rows", len(dup))
	}
}

// A scoped default check executes only the stale subjects bound to the
// named requirements while fresh records keep serving for the whole
// tree: an out-of-scope fresh requirement stays covered by its served
// evidence, an out-of-scope stale one classes scope-blocked, the
// verdict is flagged partial and never fails on the scope boundary
// alone, prune residue a global pass would derive is not derived, and
// misuse refuses - unknown identifiers and composition with full
// (REQ-check-verdict's scoped class).
func TestCheckScopedIdsExecutesOnlyInScopeStale(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	files := baseTree(map[string]string{
		".stipulator/policy.textproto": racePolicy,
		"specs/check.md": "# Check\n\n**REQ-fix-a** (behavior): The fixture MUST double.\n\n" +
			"**REQ-fix-b** (behavior): The fixture MUST triple.\n",
		"tri/tri.go":      "package tri\n\nfunc Triple(n int) int { return 3 * n }\n",
		"tri/tri_test.go": "package tri\n\nimport \"testing\"\n\nfunc TestTriple(t *testing.T) {\n\tif Triple(2) != 6 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
		// A fired-manual gap on the in-scope requirement: once REQ-fix-a
		// is covered the record is resolved, so a global pass derives it
		// as prune residue - the control proving the scoped pass's empty
		// residue is suppression, not an empty fixture.
		".stipulator/gaps/fix-a.textproto": "requirement_id: \"REQ-fix-a\"\n" +
			"reason: \"witness pending\"\n" +
			"lands {\n  manual {\n    condition: \"judged done\"\n    fired: true\n  }\n}\n",
	})
	dir := writeTree(t, files)

	spec, diags, err := compile.Compile(os.DirFS(dir))
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	hashOf := map[string]string{}
	for _, r := range spec.GetRequirements() {
		hashOf[r.GetId()] = r.GetContentHash()
	}
	gb, err := golang.NewOwned(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_, shapeA, err := gb.Resolve("example.com/checkfix/ok.TestDouble")
	if err != nil {
		t.Fatal(err)
	}
	_, shapeB, err := gb.Resolve("example.com/checkfix/tri.TestTriple")
	gb.Close()
	if err != nil {
		t.Fatal(err)
	}
	binding := func(req, sym, shape string) string {
		return "bindings {\n" +
			"  requirement_id: \"" + req + "\"\n" +
			"  content_hash: \"" + hashOf[req] + "\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"" + sym + "\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"  shape_hash: \"" + shape + "\"\n" +
			"}\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, ".stipulator", "bindings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileUnder(dir, ".stipulator/bindings/a.textproto", binding("REQ-fix-a", "example.com/checkfix/ok.TestDouble", shapeA)); err != nil {
		t.Fatal(err)
	}
	if err := writeFileUnder(dir, ".stipulator/bindings/b.textproto", binding("REQ-fix-b", "example.com/checkfix/tri.TestTriple", shapeB)); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), dir, true, nil); err != nil {
		t.Fatal(err)
	}

	rows := func(res *stipulatorv1.CheckResult) (rowA, rowB *stipulatorv1.RequirementCoverage) {
		for _, r := range res.GetCoverage().GetRequirements() {
			switch r.GetId() {
			case "REQ-fix-a":
				rowA = r
			case "REQ-fix-b":
				rowB = r
			}
		}
		if rowA == nil || rowB == nil {
			t.Fatal("coverage rows missing")
		}
		return rowA, rowB
	}

	// Leg one: only the in-scope witness is stale. The scoped pass
	// executes it and nothing else, while the out-of-scope requirement
	// stays covered by its served fresh evidence - serving is whole-tree.
	if err := writeFileUnder(dir, "ok/ok.go", fixtureOK+"\n// moved\n"); err != nil {
		t.Fatal(err)
	}
	scoped, err := Run(context.Background(), dir, false, []string{"REQ-fix-a"})
	if err != nil {
		t.Fatal(err)
	}
	if !scoped.GetScopePartial() || len(scoped.GetScopeIds()) != 1 {
		t.Fatalf("scope flags = partial=%t ids=%v, want the scoped class named", scoped.GetScopePartial(), scoped.GetScopeIds())
	}
	if scoped.GetTestsExecuted() != 1 {
		t.Fatalf("executed=%d, want exactly the in-scope stale witness", scoped.GetTestsExecuted())
	}
	if scoped.GetTestsServed() < 1 {
		t.Fatalf("served=%d, want the fresh out-of-scope witness served", scoped.GetTestsServed())
	}
	rowA, rowB := rows(scoped)
	if rowA.GetBucket() != stipulatorv1.Bucket_BUCKET_COVERED || rowA.GetScopeBlocked() {
		t.Fatalf("in-scope row = %v blocked=%t, want covered by the executed witness", rowA.GetBucket(), rowA.GetScopeBlocked())
	}
	if rowB.GetBucket() != stipulatorv1.Bucket_BUCKET_COVERED || rowB.GetScopeBlocked() {
		t.Fatalf("out-of-scope fresh row = %v blocked=%t, want covered by served evidence", rowB.GetBucket(), rowB.GetScopeBlocked())
	}
	if !scoped.GetPassed() {
		t.Error("scoped verdict failed with every row covered")
	}
	if len(scoped.GetPruneResidue()) != 0 {
		t.Error("scoped pass derived prune residue")
	}

	// Leg two: both witnesses stale. The out-of-scope one is deliberately
	// not executed and its requirement classes scope-blocked, never
	// failing the scoped verdict on the boundary alone.
	if err := writeFileUnder(dir, "ok/ok.go", fixtureOK+"\n// moved twice\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFileUnder(dir, "tri/tri.go", "package tri\n\nfunc Triple(n int) int { return 3 * n }\n\n// moved\n"); err != nil {
		t.Fatal(err)
	}
	scoped, err = Run(context.Background(), dir, false, []string{"REQ-fix-a"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.GetTestsExecuted() != 1 {
		t.Fatalf("executed=%d, want exactly the in-scope stale witness", scoped.GetTestsExecuted())
	}
	rowA, rowB = rows(scoped)
	if rowA.GetBucket() != stipulatorv1.Bucket_BUCKET_COVERED || rowA.GetScopeBlocked() {
		t.Fatalf("in-scope row = %v blocked=%t, want covered by the executed witness", rowA.GetBucket(), rowA.GetScopeBlocked())
	}
	if !rowB.GetScopeBlocked() {
		t.Fatalf("out-of-scope row = %v blocked=%t reasons=%v, want scope-blocked", rowB.GetBucket(), rowB.GetScopeBlocked(), rowB.GetReasons())
	}
	if !scoped.GetPassed() {
		t.Error("scoped verdict failed on a scope-boundary red alone")
	}
	if len(scoped.GetPruneResidue()) != 0 {
		t.Error("scoped pass derived prune residue")
	}

	// Control: the same tree's global pass DOES derive the resolved gap
	// as residue - the scoped legs' empty residue is suppression.
	global, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range global.GetPruneResidue() {
		if strings.HasSuffix(p, "fix-a.textproto") {
			found = true
		}
	}
	if !found {
		t.Fatalf("global residue = %v, want the resolved gap record", global.GetPruneResidue())
	}

	if _, err := Run(context.Background(), dir, false, []string{"REQ-nope"}); err == nil {
		t.Error("unknown identifier accepted")
	}
	if _, err := Run(context.Background(), dir, true, []string{"REQ-fix-a"}); err == nil {
		t.Error("full+ids accepted")
	}
}
