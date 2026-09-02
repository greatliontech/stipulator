package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
	"github.com/greatliontech/stipulator/stipulate"
)

// bindPinned authors one tests-role binding through the authoring path
// the CLI uses, so the content and shape pins are captured for real and
// the requirement's only possible red is the witness itself.
func bindPinned(t *testing.T, ctx context.Context, dir, req, symbol string) {
	t.Helper()
	gb, err := golang.NewOwned(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer gb.Close()
	up, err := author.Bind(os.DirFS(dir), map[string]verify.Backend{"go": gb}, author.BindRequest{
		Requirement: req,
		Symbol:      symbol,
		Backend:     "go",
		Role:        stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
	})
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, filepath.FromSlash(up.Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, up.Content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// An executed test the run watched fail is a verdict input on the
// witness-evidence form whatever it is bound to: a package one race leg
// witnesses and one plain leg also selects executes its whole
// obligation set every run, and an unbound sibling failing there reds
// the check even though the bound witness — re-granted solo by the
// isolation pass — keeps its requirement covered. The summary's
// headings and the digest count carry the cause (REQ-check-verdict's
// observed-red term, REQ-check-diagnostics).
func TestCheckDefaultObservedUnboundFailureFailsTheCheck(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-check-diagnostics")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	plainThenRace := "invocations {\n  name: \"plain\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./ok\"\n  }\n}\n" + racePolicy
	files := baseTree(map[string]string{
		"specs/check.md":               "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		"ok/golden_test.go":            "package ok\n\nimport \"testing\"\n\nfunc TestGolden(t *testing.T) {\n\tt.Fatal(\"golden moved\")\n}\n",
		".stipulator/policy.textproto": plainThenRace,
	})
	dir := writeTree(t, files)
	ctx := context.Background()
	bindPinned(t, ctx, dir, "REQ-fix-must", "example.com/checkfix/ok.TestDouble")

	res, err := Run(ctx, dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetSuiteHealthJudged() {
		t.Fatal("default check claimed suite health")
	}
	// The bound witness stayed green: the only red the run saw is the
	// unbound sibling's.
	for _, row := range res.GetCoverage().GetRequirements() {
		if row.GetId() == "REQ-fix-must" && row.GetBucket() != stipulatorv1.Bucket_BUCKET_COVERED {
			t.Fatalf("bound witness row = %v %v, want covered so the verdict's red is the unbound sibling alone", row.GetBucket(), row.GetReasons())
		}
	}
	if len(res.GetCoverage().GetViolations()) != 0 || len(res.GetVerify().GetProblems()) != 0 || len(res.GetPruneResidue()) != 0 {
		t.Fatalf("a term other than observed red is in play: violations=%v problems=%v residue=%v",
			res.GetCoverage().GetViolations(), res.GetVerify().GetProblems(), res.GetPruneResidue())
	}
	named := false
	for _, d := range res.GetWitnessDiagnostics() {
		if d.GetPackage() == "example.com/checkfix/ok" && d.GetTest() == "TestGolden" &&
			d.GetDisposition() == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED &&
			strings.Contains(d.GetOutput(), "golden moved") {
			named = true
		}
	}
	if !named {
		t.Fatalf("diagnostics %v do not name the failed unbound test with its retained output", res.GetWitnessDiagnostics())
	}
	if res.GetPassed() {
		t.Fatal("default check passed over an executed test it watched fail")
	}
	// The bounded summary explains its own verdict: the headings name
	// the red the counts cannot.
	view, err := views.CheckView(res, "summary", nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := view.(*stipulatorv1.CheckSummary)
	if sum.GetPassed() || !strings.Contains(strings.Join(sum.GetWitnessFailureHeadings(), ";"), "failed: example.com/checkfix/ok.TestGolden") {
		t.Fatalf("summary passed=%t headings=%v, want a failing summary heading the unbound red", sum.GetPassed(), sum.GetWitnessFailureHeadings())
	}
}

// A random-seeded witness never serves: on a warm tree the default
// check serves the deterministic witness and re-executes the
// rapid-driven one, attributing the refusal as uncacheable, and the
// verdict stands on the re-execution's outcome
// (REQ-evidence-witness-freshness, REQ-check-verdict).
func TestCheckDefaultReExecutesRandomSeededWitnesses(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	files := baseTree(map[string]string{
		// The real audited harness release: the freshness engine admits
		// observation through exactly that version, so the served-or-not
		// decision under test is stipulator's own. Its own package: a
		// package reaching the harness publishes only through the
		// observation path, so the deterministic sibling keeps its
		// closure-proven serving.
		"go.mod": "module example.com/checkfix\n\ngo 1.26.4\n\nrequire pgregory.net/rapid v1.3.0\n",
		"go.sum": "pgregory.net/rapid v1.3.0 h1:vBvO0VSqti75J1jjYqpgPNBLKMd1+gxa9fYo7vk/Exc=\npgregory.net/rapid v1.3.0/go.mod h1:dPlE4OBBxgXPqkP79flB6sJL1dx5azpI7HQ9MY9Z7uk=\n",
		"prop/prop_test.go": "package prop\n\nimport (\n\t\"testing\"\n\n\t\"example.com/checkfix/ok\"\n\t\"pgregory.net/rapid\"\n)\n\n" +
			"func TestDoubleProperty(t *testing.T) {\n\trapid.Check(t, func(rt *rapid.T) {\n\t\tif ok.Double(3) != 6 {\n\t\t\tpanic(\"broken\")\n\t\t}\n\t})\n}\n",
		"specs/check.md":               "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		".stipulator/policy.textproto": racePolicy,
	})
	dir := writeTree(t, files)
	ctx := context.Background()
	bindPinned(t, ctx, dir, "REQ-fix-must", "example.com/checkfix/prop.TestDoubleProperty")

	warm, err := Run(ctx, dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !warm.GetPassed() || warm.GetTestsExecuted() != 2 {
		t.Fatalf("full check passed=%t executed=%d, want a passing run of both tests", warm.GetPassed(), warm.GetTestsExecuted())
	}
	const property = "example.com/checkfix/prop.TestDoubleProperty"
	if !strings.Contains(warm.GetUncacheableReasons()[property], "random-seeded") {
		t.Fatalf("full-form uncacheable reasons = %v, want the seeded witness attributed", warm.GetUncacheableReasons())
	}

	served, err := Run(ctx, dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if served.GetTestsServed() != 1 || served.GetTestsExecuted() != 1 {
		t.Fatalf("default check served=%d executed=%d, want the example served and the seeded witness re-executed",
			served.GetTestsServed(), served.GetTestsExecuted())
	}
	if reasons := served.GetUncacheableReasons(); len(reasons) != 1 || !strings.Contains(reasons[property], "random-seeded") {
		t.Fatalf("default-form uncacheable reasons = %v, want exactly the seeded witness attributed", reasons)
	}
	if !served.GetPassed() {
		t.Fatal("default check failed on the warm passing tree")
	}
	for _, row := range served.GetCoverage().GetRequirements() {
		if row.GetId() == "REQ-fix-must" && row.GetBucket() != stipulatorv1.Bucket_BUCKET_COVERED {
			t.Fatalf("seeded witness row = %v %v, want covered by its re-execution", row.GetBucket(), row.GetReasons())
		}
	}
}

// A caller-named scope narrows the every-run ineligible legs exactly as
// it narrows the stale selection: an unbound test red in a package the
// scope excludes is never executed, so the partial verdict cannot fail
// on it, while the unscoped check over the same tree observes the red
// and fails (REQ-check-verdict's scoped class and observed-red term).
func TestCheckScopedIdsLeaveOutOfScopeIneligibleLegsUnexecuted(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	plainOther := "invocations {\n  name: \"plain\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./other\"\n  }\n}\n"
	files := baseTree(map[string]string{
		"specs/check.md":               "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		"other/other.go":               "package other\n\nfunc Triple(x int) int { return 3 * x }\n",
		"other/other_test.go":          "package other\n\nimport \"testing\"\n\nfunc TestTriple(t *testing.T) {\n\tif Triple(1) != 3 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n\nfunc TestGolden(t *testing.T) {\n\tt.Fatal(\"golden moved\")\n}\n",
		".stipulator/policy.textproto": plainOther + racePolicy,
	})
	dir := writeTree(t, files)
	ctx := context.Background()
	bindPinned(t, ctx, dir, "REQ-fix-must", "example.com/checkfix/ok.TestDouble")

	scoped, err := Run(ctx, dir, false, []string{"REQ-fix-must"})
	if err != nil {
		t.Fatal(err)
	}
	if !scoped.GetScopePartial() || !scoped.GetPassed() {
		t.Fatalf("scoped check partial=%t passed=%t, want a passing partial verdict untouched by the out-of-scope red", scoped.GetScopePartial(), scoped.GetPassed())
	}
	for _, d := range scoped.GetWitnessDiagnostics() {
		if d.GetPackage() == "example.com/checkfix/other" {
			t.Fatalf("scoped check executed the out-of-scope ineligible leg: %v", d)
		}
	}

	global, err := Run(ctx, dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if global.GetPassed() {
		t.Fatal("unscoped check passed over the red the ineligible leg observed")
	}
}
