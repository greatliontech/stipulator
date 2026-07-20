package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/wire"
	"github.com/greatliontech/stipulator/stipulate"
)

// failingCheckResult builds a check result carrying one assertion failure
// and one degraded execution, the pair whose renderings must never be
// conflated.
func failingCheckResult() *stipulatorv1.CheckResult {
	failed := &stipulatorv1.FailureDiagnostic{}
	failed.SetInvocation("race")
	failed.SetPackage("example.com/m/red")
	failed.SetTest("TestRed")
	failed.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	failed.SetOutput("    red_test.go:7: boom\n")
	degraded := &stipulatorv1.FailureDiagnostic{}
	degraded.SetInvocation("race")
	degraded.SetPackage("example.com/m/silent")
	degraded.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	degraded.SetOutput("test binary exited without a report\n")

	inv := &stipulatorv1.InvocationHealth{}
	inv.SetInvocation("race")
	inv.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	ex := &stipulatorv1.ExecutionReport{}
	ex.SetInvocations([]*stipulatorv1.InvocationHealth{inv})
	ex.SetDiagnostics([]*stipulatorv1.FailureDiagnostic{failed, degraded})

	res := &stipulatorv1.CheckResult{}
	res.SetPassed(false)
	res.SetExecution(ex)
	res.SetTestsExecuted(2)
	res.SetTestsUncacheable(2)
	return res
}

// TestCheckRenderNamesPolicyRecordPath pins the policy-problem
// projection: the rendering names the committed record's path beside the
// message, exactly as compile problems name their documents — a
// validation error's message alone never locates the record.
func TestCheckRenderNamesPolicyRecordPath(t *testing.T) {
	stipulate.Covers(t, "REQ-check-diagnostics")
	p := &stipulatorv1.Problem{}
	p.SetPath(".stipulator/policy.textproto")
	p.SetMessage("invocation \"race\": missing explicit timeout")
	res := &stipulatorv1.CheckResult{}
	res.SetPolicyProblem(p)
	var stdout, stderr bytes.Buffer
	renderCheck(&stdout, &stderr, res)
	if !strings.Contains(stderr.String(), ".stipulator/policy.textproto: invocation") {
		t.Errorf("policy problem rendering does not name the record path:\n%s", stderr.String())
	}
}

func TestCheckRenderNamesDegradedDistinctly(t *testing.T) {
	stipulate.Covers(t, "REQ-check-diagnostics")
	var stdout, stderr bytes.Buffer
	renderCheck(&stdout, &stderr, failingCheckResult())
	diag := stderr.String()
	if !strings.Contains(diag, "failed: example.com/m/red.TestRed") {
		t.Errorf("assertion failure not named as failed:\n%s", diag)
	}
	if !strings.Contains(diag, "    red_test.go:7: boom") {
		t.Errorf("failed witness output not surfaced:\n%s", diag)
	}
	if !strings.Contains(diag, "degraded: example.com/m/silent") {
		t.Errorf("degraded execution not named distinctly:\n%s", diag)
	}
	if !strings.Contains(diag, "test binary exited without a report") {
		t.Errorf("degraded execution output not surfaced:\n%s", diag)
	}
	if !strings.Contains(stdout.String(), "check: fail") {
		t.Errorf("no verdict line:\n%s", stdout.String())
	}
}

func TestCheckJSONProjectionIsDeterministic(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	// The exact-bytes pin is the determinism claim: protojson randomizes
	// whitespace only across binaries, so a same-process double render
	// would pass even without canonicalization.
	res := &stipulatorv1.CheckResult{}
	res.SetPassed(true)
	res.SetTestsExecuted(2)
	res.SetTestsUncacheable(1)
	res.SetPruneResidue([]string{".stipulator/gaps/x.textproto"})
	got, err := wire.CanonicalJSON(res)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"passed\": true,\n" +
		"  \"pruneResidue\": [\n" +
		"    \".stipulator/gaps/x.textproto\"\n" +
		"  ],\n" +
		"  \"testsExecuted\": 2,\n" +
		"  \"testsUncacheable\": 1\n" +
		"}\n"
	if string(got) != want {
		t.Errorf("canonical projection drifted:\n%s\nwant:\n%s", got, want)
	}
	decoded := &stipulatorv1.CheckResult{}
	if err := protojson.Unmarshal(got, decoded); err != nil {
		t.Fatalf("JSON projection is not a strict CheckResult: %v", err)
	}
	full, err := wire.CanonicalJSON(failingCheckResult())
	if err != nil {
		t.Fatal(err)
	}
	if err := protojson.Unmarshal(full, &stipulatorv1.CheckResult{}); err != nil {
		t.Fatalf("full-result projection is not a strict CheckResult: %v", err)
	}
}

// TestCheckExitCodes pins the command's exit-code contract through the
// built binary: 0 for a passing tree, 1 for a tree that fails the check,
// 2 for an operational error — CI can distinguish a red tree from a
// broken run.
func TestCheckExitCodes(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("builds the CLI and executes policies over fixture trees")
	}
	bin := filepath.Join(t.TempDir(), "stipulator")
	build := exec.Command("go", "build", "-o", bin, "github.com/greatliontech/stipulator/cmd/stipulator")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}

	writeTree := func(files map[string]string) string {
		dir := t.TempDir()
		for path, content := range files {
			full := filepath.Join(dir, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	base := map[string]string{
		"go.mod":                         "module example.com/checkfix\n\ngo 1.26.4\n",
		"ok/ok.go":                       "package ok\n\nfunc Double(x int) int { return 2 * x }\n",
		"ok/ok_test.go":                  "package ok\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) { Double(2) }\n",
		".stipulator/manifest.textproto": "include: \"specs/**/*.md\"\n",
		".stipulator/policy.textproto":   "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n  }\n}\n",
	}
	passTree := map[string]string{"specs/check.md": "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n"}
	failTree := map[string]string{"specs/check.md": "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n"}
	for path, content := range base {
		passTree[path] = content
		failTree[path] = content
	}

	env := append(os.Environ(), "GOENV=off", "GOFLAGS=", "GOPACKAGESDRIVER=", "GOTOOLCHAIN=local", "NO_COLOR=1")
	run := func(dir string, args ...string) (int, string, string) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("running check: %v\n%s", err, stderr.String())
			}
			code = ee.ExitCode()
		}
		return code, stdout.String(), stderr.String()
	}

	pass := writeTree(passTree)
	if code, _, stderr := run(pass, "check", "--quiet"); code != 0 {
		t.Errorf("passing tree exit = %d, want 0\n%s", code, stderr)
	}

	fail := writeTree(failTree)
	code, stdout, _ := run(fail, "check", "--quiet")
	if code != 1 {
		t.Errorf("failing tree exit = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("quiet mode wrote to stdout: %q", stdout)
	}
	code, stdout, _ = run(fail, "check", "--json")
	if code != 1 {
		t.Errorf("failing tree --json exit = %d, want 1", code)
	}
	decoded := &stipulatorv1.CheckResult{}
	if err := protojson.Unmarshal([]byte(stdout), decoded); err != nil {
		t.Fatalf("--json stdout is not a strict CheckResult: %v\n%s", err, stdout)
	}
	if decoded.GetPassed() {
		t.Error("JSON verdict passed on the failing tree")
	}

	if code, _, _ := run(t.TempDir(), "check"); code != 2 {
		t.Errorf("non-corpus dir exit = %d, want 2", code)
	}
}

// The serving form has no execution report, so its diagnostics render
// from the result's own typed rows — dispositions and truncation named
// exactly as the health-judged form names them.
func TestCheckRenderServingFormNamesDiagnosticsDistinctly(t *testing.T) {
	stipulate.Covers(t, "REQ-check-diagnostics")
	res := &stipulatorv1.CheckResult{}
	res.SetTestsServed(2)
	res.SetTestsExecuted(1)
	failed := &stipulatorv1.FailureDiagnostic{}
	failed.SetPackage("example.com/m/red")
	failed.SetTest("TestRed")
	failed.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	failed.SetOutput("    red_test.go:7: boom\n")
	degraded := &stipulatorv1.FailureDiagnostic{}
	degraded.SetPackage("example.com/m/silent")
	degraded.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	degraded.SetOutput("test binary exited without a report\n")
	degraded.SetTruncated(true)
	res.SetWitnessDiagnostics([]*stipulatorv1.FailureDiagnostic{failed, degraded})

	var stdout, stderr bytes.Buffer
	renderCheck(&stdout, &stderr, res)
	diag := stderr.String()
	if !strings.Contains(diag, "witnessed: 2 served fresh, 1 executed") {
		t.Errorf("serving line missing:\n%s", diag)
	}
	if !strings.Contains(diag, "failed: example.com/m/red.TestRed") {
		t.Errorf("assertion failure not named as failed:\n%s", diag)
	}
	if !strings.Contains(diag, "degraded: example.com/m/silent") {
		t.Errorf("degraded execution not named distinctly:\n%s", diag)
	}
	if !strings.Contains(diag, "(output truncated)") {
		t.Errorf("truncation marker lost:\n%s", diag)
	}
}

// The uncacheable histogram aggregates per-test reasons into a bounded
// frequency view, most common first — the diagnosis instrument for a
// cache that will not warm, never a per-test flood.
func TestCheckRenderUncacheableHistogram(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	res := &stipulatorv1.CheckResult{}
	res.SetTestsServed(1)
	res.SetTestsExecuted(3)
	res.SetTestsUncacheable(3)
	res.SetUncacheableReasons(map[string]string{
		"p.TestA": "observation sealed: runtime input not covered by observation bracket: x.txt",
		"p.TestB": "observation sealed: runtime input not covered by observation bracket: x.txt",
		"p.TestC": "no healthy process granted the outcome",
	})
	var stdout, stderr bytes.Buffer
	renderCheck(&stdout, &stderr, res)
	out := stderr.String()
	first := strings.Index(out, "2  observation sealed: runtime input not covered by observation bracket: x.txt")
	second := strings.Index(out, "1  no healthy process granted the outcome")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("histogram missing or misordered:\n%s", out)
	}
}
