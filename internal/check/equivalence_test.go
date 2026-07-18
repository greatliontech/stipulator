package check

import (
	"context"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/stipulate"
)

// The unified check's failure-class conservation (REQ-policy-conservation,
// REQ-check-verdict) is pinned end to end through Run: every class a
// complete suite checks must flip the one verdict. Each class is pinned by
// exactly one test in this package:
//
//	failure class              pinned by
//	package build failure      TestCheckCatchesEverySuiteFailureClass/"package build failure"
//	init failure               TestCheckCatchesEverySuiteFailureClass/"init failure"
//	TestMain failure           TestCheckCatchesEverySuiteFailureClass/"TestMain failure"
//	red executable example     TestCheckCatchesEverySuiteFailureClass/"red executable example"
//	red committed fuzz seed    TestCheckCatchesEverySuiteFailureClass/"red committed fuzz seed"
//	broken no-test package     TestCheckCatchesEverySuiteFailureClass/"broken no-test package"
//	workspace member failure   TestCheckCatchesEverySuiteFailureClass/"workspace member failure"
//	failing named test         TestCheckSuiteFailureFailsTheCheckWithDiagnostics
//	binding failure            TestCheckBrokenBindingFailsTheCheck
//	coverage violation         TestCheckCoverageViolationFailsTheCheck
//	prune residue              TestCheckWitnessResolvedGapIsResidueUntilPruned
//
// The references below make each standing pin load-bearing at compile
// time: a class cannot silently lose its test.
var (
	_ func(*testing.T) = TestCheckSuiteFailureFailsTheCheckWithDiagnostics
	_ func(*testing.T) = TestCheckBrokenBindingFailsTheCheck
	_ func(*testing.T) = TestCheckCoverageViolationFailsTheCheck
	_ func(*testing.T) = TestCheckWitnessResolvedGapIsResidueUntilPruned
)

// checkUnhealthy runs the check over a tree and requires the verdict to
// flip on suite health alone: the coverage gate stays green — nothing in
// the fixture is bound — so a passing check would mean the class was
// silently dropped from the suite.
func checkUnhealthy(t *testing.T, files map[string]string) *stipulatorv1.CheckResult {
	t.Helper()
	dir := writeTree(t, files)
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed; the failure class was dropped from the suite")
	}
	if !res.GetCoverage().GetGatePasses() {
		t.Errorf("gate leg red too; the scenario no longer isolates suite health: %v", res.GetCoverage().GetViolations())
	}
	if golang.SuiteHealthy(res.GetExecution()) {
		t.Error("execution report reads healthy despite the fixture failure")
	}
	return res
}

// reportDiagnostic finds one failure diagnostic by package and test name.
func reportDiagnostic(res *stipulatorv1.CheckResult, pkg, test string) *stipulatorv1.FailureDiagnostic {
	for _, d := range res.GetExecution().GetDiagnostics() {
		if d.GetPackage() == pkg && d.GetTest() == test {
			return d
		}
	}
	return nil
}

// reportTest finds one named test outcome by package and test name.
func reportTest(res *stipulatorv1.CheckResult, pkg, test string) *stipulatorv1.TestResult {
	for _, tr := range res.GetExecution().GetTests() {
		if tr.GetPackage() == pkg && tr.GetTest() == test {
			return tr
		}
	}
	return nil
}

// TestCheckCatchesEverySuiteFailureClass proves, class by class, that one
// execution of the accepted test policy conserves the failure classes a
// complete suite checks: each subtest plants exactly one class in an
// otherwise-passing tree and requires the unified verdict to fail with the
// class attributed in the retained diagnostics.
func TestCheckCatchesEverySuiteFailureClass(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-policy-conservation")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree per class")
	}
	neutralAmbient(t)

	t.Run("package build failure", func(t *testing.T) {
		res := checkUnhealthy(t, baseTree(map[string]string{
			"builderr/builderr.go": "package builderr\n\nfunc broken() { undefinedIdentifier() }\n",
			"builderr/builderr_test.go": "package builderr\n\nimport \"testing\"\n\n" +
				"func TestNeverBuilds(t *testing.T) {}\n",
		}))
		d := reportDiagnostic(res, "example.com/checkfix/builderr", "")
		if d == nil {
			t.Fatal("no package diagnostic for the build failure")
		}
		if d.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
			t.Errorf("diagnostic disposition = %v, want BUILD_FAILED", d.GetDisposition())
		}
		if !strings.Contains(d.GetOutput(), "undefinedIdentifier") {
			t.Errorf("compiler output not retained: %q", d.GetOutput())
		}
	})

	t.Run("init failure", func(t *testing.T) {
		res := checkUnhealthy(t, baseTree(map[string]string{
			"initred/initred_test.go": "package initred\n\nimport \"testing\"\n\n" +
				"func init() {\n\tpanic(\"init red\")\n}\n\n" +
				"func TestNeverRuns(t *testing.T) {}\n",
		}))
		if tr := reportTest(res, "example.com/checkfix/initred", "TestNeverRuns"); tr != nil {
			t.Errorf("a test that never ran gained an outcome: %v", tr)
		}
		d := reportDiagnostic(res, "example.com/checkfix/initred", "")
		if d == nil || !strings.Contains(d.GetOutput(), "panic: init red") {
			t.Errorf("init panic not retained in the package diagnostic: %v", d)
		}
	})

	t.Run("TestMain failure", func(t *testing.T) {
		res := checkUnhealthy(t, baseTree(map[string]string{
			"redmain/redmain_test.go": "package redmain\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
				"func TestMain(m *testing.M) {\n\tm.Run()\n\tos.Exit(1)\n}\n\n" +
				"func TestGreen(t *testing.T) {}\n",
		}))
		// Exit-behavior conservation: the red exit fails the package while
		// the green outcomes it produced remain recorded.
		if tr := reportTest(res, "example.com/checkfix/redmain", "TestGreen"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
			t.Errorf("TestGreen outcome = %v, want PASSED recorded beside the red package", tr)
		}
		if d := reportDiagnostic(res, "example.com/checkfix/redmain", ""); d == nil {
			t.Error("no package diagnostic for the red TestMain exit")
		}
	})

	t.Run("red executable example", func(t *testing.T) {
		res := checkUnhealthy(t, baseTree(map[string]string{
			"examples/examples.go": "// Package examples carries executable examples.\npackage examples\n",
			"examples/examples_test.go": "package examples\n\nimport \"fmt\"\n\n" +
				"func Example_fail() {\n\tfmt.Println(\"actual output\")\n\t// Output: something else entirely\n}\n",
		}))
		if tr := reportTest(res, "example.com/checkfix/examples", "Example_fail"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
			t.Errorf("Example_fail outcome = %v, want FAILED", tr)
		}
		d := reportDiagnostic(res, "example.com/checkfix/examples", "Example_fail")
		if d == nil || !strings.Contains(d.GetOutput(), "actual output") {
			t.Errorf("example mismatch output not retained: %v", d)
		}
	})

	t.Run("red committed fuzz seed", func(t *testing.T) {
		res := checkUnhealthy(t, baseTree(map[string]string{
			"fuzzseed/fuzzseed.go": "// Package fuzzseed carries a fuzz target whose committed seed fails replay.\n" +
				"package fuzzseed\n\n// Refuses reports whether s is the refused value.\nfunc Refuses(s string) bool { return s == \"bad\" }\n",
			"fuzzseed/fuzzseed_test.go": "package fuzzseed\n\nimport \"testing\"\n\n" +
				"func FuzzRefuse(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, s string) {\n" +
				"\t\tif Refuses(s) {\n\t\t\tt.Fatal(\"committed seed fails replay\")\n\t\t}\n\t})\n}\n",
			"fuzzseed/testdata/fuzz/FuzzRefuse/seed-red": "go test fuzz v1\nstring(\"bad\")\n",
		}))
		if tr := reportTest(res, "example.com/checkfix/fuzzseed", "FuzzRefuse/seed-red"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
			t.Errorf("seed replay outcome = %v, want the named seed FAILED", tr)
		}
	})

	t.Run("broken no-test package", func(t *testing.T) {
		// No test names this package, so an execution that only ran named
		// bound tests would never notice it; the suite's package scope must.
		res := checkUnhealthy(t, baseTree(map[string]string{
			"notest/notest.go": "package notest\n\nfunc broken() { undefinedIdentifier() }\n",
		}))
		d := reportDiagnostic(res, "example.com/checkfix/notest", "")
		if d == nil {
			t.Fatal("no package diagnostic for the no-test package's build failure")
		}
		if d.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
			t.Errorf("diagnostic disposition = %v, want BUILD_FAILED", d.GetDisposition())
		}
	})

	t.Run("workspace member failure", func(t *testing.T) {
		files := baseTree(map[string]string{
			"go.work":       "go 1.26.4\n\nuse (\n\t.\n\t./member\n)\n",
			"member/go.mod": "module example.com/checkmember\n\ngo 1.26.4\n",
			"member/member.go": "// Package member is a workspace member whose suite fails.\npackage member\n\n" +
				"// Answer is wrong on purpose.\nfunc Answer() int { return 41 }\n",
			"member/member_test.go": "package member\n\nimport \"testing\"\n\n" +
				"func TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(\"workspace member failure\")\n\t}\n}\n",
		})
		files[".stipulator/policy.textproto"] = "invocations {\n  name: \"member\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    module_root: \"member\"\n    packages: \"./...\"\n  }\n}\n" +
			"invocations {\n  name: \"root\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n  }\n}\n"
		res := checkUnhealthy(t, files)
		d := reportDiagnostic(res, "example.com/checkmember", "TestAnswer")
		if d == nil {
			t.Fatal("no diagnostic for the failing member test")
		}
		if d.GetInvocation() != "member" {
			t.Errorf("failure attributed to invocation %q, want the member's own invocation", d.GetInvocation())
		}
		for _, inv := range res.GetExecution().GetInvocations() {
			if inv.GetInvocation() == "root" && inv.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				t.Errorf("root invocation disposition = %v; the member's failure must stay its own", inv.GetDisposition())
			}
		}
	})
}
