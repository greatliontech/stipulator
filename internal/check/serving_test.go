package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
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

	warm, err := Run(context.Background(), dir, true)
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

	served, err := Run(context.Background(), dir, false)
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
	if _, err := Run(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}

	// Behavior-preserving byte change: the closure hash moves, the test
	// stays green.
	if err := writeFileUnder(dir, "ok/ok.go", fixtureOK+"\n// moved\n"); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), dir, false)
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
	if _, err := Run(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}

	if err := writeFileUnder(dir, "ok/ok.go", "package ok\n\nfunc Double(x int) int { return 3 * x }\n"); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), dir, false)
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
}
