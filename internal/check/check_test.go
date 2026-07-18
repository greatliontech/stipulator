package check

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/progress"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// writeTree lays out a temporary corpus tree from path→content pairs.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
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

// neutralAmbient pins the ambient controls policy normalization reads to
// a known hermetic state, so host configuration cannot steer these tests.
func neutralAmbient(t *testing.T) {
	t.Helper()
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPACKAGESDRIVER", "")
	t.Setenv("GOTOOLCHAIN", "local")
}

const (
	fixtureGoMod  = "module example.com/checkfix\n\ngo 1.26.4\n"
	fixtureOK     = "package ok\n\nfunc Double(x int) int { return 2 * x }\n"
	fixtureOKTest = "package ok\n\nimport \"testing\"\n\n" +
		"func TestDouble(t *testing.T) {\n\tif Double(2) != 4 {\n\t\tt.Fatal(\"broken arithmetic\")\n\t}\n}\n"
	fixtureManifest = "include: \"specs/**/*.md\"\n"
	// plainPolicy executes ./... once without the race detector: suite
	// health without Go witness evidence, the cheap configuration for
	// scenarios that do not need a witness.
	plainPolicy = "invocations {\n  name: \"all\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n  }\n}\n"
	// racePolicy executes ./... once under the race detector, the
	// configuration witness evidence requires.
	racePolicy = "invocations {\n  name: \"race\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n"
)

// baseTree is a corpus whose check passes with plainPolicy: one exempt
// MAY requirement, one healthy package.
func baseTree(extra map[string]string) map[string]string {
	files := map[string]string{
		"go.mod":                         fixtureGoMod,
		"ok/ok.go":                       fixtureOK,
		"ok/ok_test.go":                  fixtureOKTest,
		"specs/check.md":                 "# Check\n\n**REQ-fix-may** (behavior): The fixture MAY pass.\n",
		".stipulator/manifest.textproto": fixtureManifest,
		".stipulator/policy.textproto":   plainPolicy,
	}
	for path, content := range extra {
		files[path] = content
	}
	return files
}

func TestCheckMissingPolicyFailsWithGuidance(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	files := baseTree(nil)
	delete(files, ".stipulator/policy.textproto")
	dir := writeTree(t, files)
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed without an accepted test policy")
	}
	p := res.GetPolicyProblem()
	if p == nil {
		t.Fatal("no policy problem reported")
	}
	if !strings.Contains(p.GetMessage(), "stipulator policy init") {
		t.Errorf("policy problem carries no guidance: %q", p.GetMessage())
	}
	if res.GetExecution() != nil {
		t.Error("execution section present although no policy could load")
	}
}

// TestCheckEmptyPolicyFailsWithNamedCause pins the zero-invocation
// refusal: a canonical-looking record accepting no test work names no
// suite whose health the verdict could judge, so the check fails as a
// policy problem stating the cause — never as a bare unhealthy suite
// with nothing to print.
func TestCheckEmptyPolicyFailsWithNamedCause(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-policy-record-location")
	files := baseTree(nil)
	files[".stipulator/policy.textproto"] = "# empty on purpose\n"
	dir := writeTree(t, files)
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed under a policy declaring no invocations")
	}
	p := res.GetPolicyProblem()
	if p == nil {
		t.Fatal("no policy problem reported")
	}
	if !strings.Contains(p.GetMessage(), "no invocations") {
		t.Errorf("policy problem does not name the cause: %q", p.GetMessage())
	}
}

// TestCheckUnreadablePolicyIsOperational pins the record/operational
// split: a read fault that says nothing about the record's content is an
// error, never a verdict about the tree.
func TestCheckUnreadablePolicyIsOperational(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind for root")
	}
	files := baseTree(nil)
	dir := writeTree(t, files)
	if err := os.Chmod(filepath.Join(dir, ".stipulator/policy.textproto"), 0); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), dir)
	if err == nil {
		t.Fatal("unreadable policy record produced no operational error")
	}
	if res != nil {
		t.Errorf("unreadable policy record produced a verdict: %v", res)
	}
}

func TestCheckCompileFailureIsTheVerdict(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md": "# Check\n\n**REQ-fix-dup** (behavior): The fixture MUST pass.\n\n" +
			"**REQ-fix-dup** (behavior): The fixture MUST pass twice.\n",
	}))
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed on a corpus that does not compile")
	}
	if len(res.GetCompileProblems()) == 0 {
		t.Fatal("no compile problems reported")
	}
	if res.GetExecution() != nil || res.GetVerify() != nil || res.GetCoverage() != nil {
		t.Error("later sections present although the corpus did not compile")
	}
}

func TestCheckSuiteFailureFailsTheCheckWithDiagnostics(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-check-diagnostics")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		"red/red_test.go": "package red\n\nimport \"testing\"\n\n" +
			"func TestAlwaysRed(t *testing.T) {\n\tt.Fatal(\"deliberate fixture failure\")\n}\n",
	}))
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed on an unhealthy suite")
	}
	// The failing test is unbound, so no coverage leg is red: the verdict
	// flips on suite health alone.
	if !res.GetCoverage().GetGatePasses() {
		t.Errorf("gate leg red too; the scenario no longer isolates suite health: %v", res.GetCoverage().GetViolations())
	}
	if golang.SuiteHealthy(res.GetExecution()) {
		t.Error("execution report reads healthy despite the red test")
	}
	var found bool
	for _, d := range res.GetExecution().GetDiagnostics() {
		if d.GetTest() == "TestAlwaysRed" {
			found = true
			if !strings.Contains(d.GetOutput(), "deliberate fixture failure") {
				t.Errorf("diagnostic retains no failure output: %q", d.GetOutput())
			}
			if d.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
				t.Errorf("assertion failure disposition = %v", d.GetDisposition())
			}
		}
	}
	if !found {
		t.Error("no diagnostic for the failed witness")
	}
}

func TestCheckCoverageViolationFailsTheCheck(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md": "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
	}))
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed with an undeclared uncovered requirement")
	}
	if !golang.SuiteHealthy(res.GetExecution()) {
		t.Error("suite leg red too; the scenario no longer isolates the gate")
	}
	violations := res.GetCoverage().GetViolations()
	if len(violations) != 1 || violations[0] != "REQ-fix-must" {
		t.Errorf("violations = %v, want [REQ-fix-must]", violations)
	}
}

func TestCheckVerifyProblemFailsTheCheck(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/bindings/ghost.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-ghost\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.Double\"\n" +
			"  role: BINDING_ROLE_IMPLEMENTS\n" +
			"}\n",
	}))
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed despite a verification problem")
	}
	if len(res.GetVerify().GetProblems()) == 0 {
		t.Fatal("no verification problem reported for the dangling binding")
	}
	if !golang.SuiteHealthy(res.GetExecution()) {
		t.Error("suite leg red too; the scenario no longer isolates verification")
	}
}

func TestCheckBrokenBindingFailsTheCheck(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md": "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		".stipulator/bindings/broken.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-must\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestGone\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	res, err := Run(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed despite a broken binding")
	}
	var bucket stipulatorv1.Bucket
	for _, r := range res.GetCoverage().GetRequirements() {
		if r.GetId() == "REQ-fix-must" {
			bucket = r.GetBucket()
		}
	}
	if bucket != stipulatorv1.Bucket_BUCKET_BROKEN {
		t.Errorf("REQ-fix-must bucket = %v, want BROKEN", bucket)
	}
	if len(res.GetCoverage().GetViolations()) == 0 {
		t.Error("broken requirement with no gap raised no violation")
	}
}

// TestCheckWitnessResolvedGapIsResidueUntilPruned pins the folded prune
// lint blind spot: a gap whose requirement reaches covered only through an
// executed witness is invisible to an unwitnessed evaluation, but the
// check evaluates gaps inside its witnessed single pass, so the lingering
// record is prune residue and the verdict fails until the record is
// deleted. The fixture's manual condition is explicitly fired: coverage
// resolves a manual-condition gap only alongside the fired external
// judgment (an unfired one stays open — the lifecycle's manual arm,
// pinned separately).
func TestCheckWitnessResolvedGapIsResidueUntilPruned(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-gap-resolved-pruned")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a fixture tree")
	}
	neutralAmbient(t)
	gapPath := ".stipulator/gaps/fix-must.textproto"
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md":               "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		".stipulator/policy.textproto": racePolicy,
		gapPath: "requirement_id: \"REQ-fix-must\"\n" +
			"reason: \"witness pending\"\n" +
			"lands {\n  manual {\n    condition: \"judged done\"\n    fired: true\n  }\n}\n",
	}))
	// Author the witness binding through the same authoring path the CLI
	// uses, so the content and shape pins are captured for real.
	ctx := context.Background()
	gb, err := golang.NewContext(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	up, err := author.Bind(os.DirFS(dir), map[string]verify.Backend{"go": gb}, author.BindRequest{
		Requirement: "REQ-fix-must",
		Symbol:      "example.com/checkfix/ok.TestDouble",
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

	res, err := Run(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed while a resolved gap record lingers")
	}
	if got := res.GetPruneResidue(); len(got) != 1 || got[0] != gapPath {
		t.Fatalf("prune residue = %v, want [%s]", got, gapPath)
	}
	var state stipulatorv1.GapState
	for _, g := range res.GetCoverage().GetGaps() {
		if g.GetRequirementId() == "REQ-fix-must" {
			state = g.GetState()
		}
	}
	if state != stipulatorv1.GapState_GAP_STATE_RESOLVED {
		t.Errorf("gap state = %v, want RESOLVED: resolution must be visible to the witnessed pass", state)
	}
	if res.GetTestsExecuted() == 0 {
		t.Error("executed count is zero although the policy ran the witness")
	}

	// Pruning the record is the whole remaining fault: the same tree then
	// passes.
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(gapPath))); err != nil {
		t.Fatal(err)
	}
	res, err = Run(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetPassed() {
		t.Errorf("check failed after pruning; verify=%v violations=%v residue=%v diagnostics=%v",
			res.GetVerify().GetProblems(), res.GetCoverage().GetViolations(),
			res.GetPruneResidue(), res.GetExecution().GetDiagnostics())
	}
	if len(res.GetPruneResidue()) != 0 {
		t.Errorf("prune residue = %v after the record was deleted", res.GetPruneResidue())
	}
}

// TestCheckUnfiredManualGapOutlivesGreenWitnesses pins the gap
// lifecycle's manual arm end to end: a covered requirement whose gap
// carries an unfired manual landing condition stays open — the check
// passes with no prune residue, so the record expresses a declared
// violation on a path no witness reaches while every bound witness is
// green. Explicitly firing the condition is what resolves the gap into
// prune residue.
func TestCheckUnfiredManualGapOutlivesGreenWitnesses(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-gap-lifecycle", "REQ-gap-conditions")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a fixture tree")
	}
	neutralAmbient(t)
	gapPath := ".stipulator/gaps/fix-must.textproto"
	unfired := "requirement_id: \"REQ-fix-must\"\n" +
		"reason: \"violated on an unwitnessed path\"\n" +
		"lands {\n  manual {\n    condition: \"the unwitnessed path is closed\"\n  }\n}\n"
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md":               "# Check\n\n**REQ-fix-must** (behavior): The fixture MUST pass.\n",
		".stipulator/policy.textproto": racePolicy,
		gapPath:                        unfired,
	}))
	ctx := context.Background()
	gb, err := golang.NewContext(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	up, err := author.Bind(os.DirFS(dir), map[string]verify.Backend{"go": gb}, author.BindRequest{
		Requirement: "REQ-fix-must",
		Symbol:      "example.com/checkfix/ok.TestDouble",
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

	res, err := Run(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	gapState := func() stipulatorv1.GapState {
		for _, g := range res.GetCoverage().GetGaps() {
			if g.GetRequirementId() == "REQ-fix-must" {
				return g.GetState()
			}
		}
		return stipulatorv1.GapState_GAP_STATE_UNSPECIFIED
	}
	if !res.GetPassed() {
		t.Errorf("check failed with a covered requirement and an unfired manual gap; verify=%v violations=%v residue=%v",
			res.GetVerify().GetProblems(), res.GetCoverage().GetViolations(), res.GetPruneResidue())
	}
	if got := gapState(); got != stipulatorv1.GapState_GAP_STATE_OPEN {
		t.Errorf("gap state = %v, want OPEN: coverage must not fire an external judgment", got)
	}
	if got := res.GetPruneResidue(); len(got) != 0 {
		t.Errorf("prune residue = %v; an unfired manual gap is load-bearing, never residue", got)
	}
	var bucket stipulatorv1.Bucket
	for _, r := range res.GetCoverage().GetRequirements() {
		if r.GetId() == "REQ-fix-must" {
			bucket = r.GetBucket()
		}
	}
	if bucket != stipulatorv1.Bucket_BUCKET_COVERED {
		t.Fatalf("bucket = %v, want COVERED: the scenario needs green witnesses under the open gap", bucket)
	}

	// Firing the condition is the external judgment: the same record then
	// resolves and lingers as prune residue.
	fired := strings.Replace(unfired, "condition: \"the unwitnessed path is closed\"\n", "condition: \"the unwitnessed path is closed\"\n    fired: true\n", 1)
	if fired == unfired {
		t.Fatal("fixture rewrite did not fire the condition")
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(gapPath)), []byte(fired), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Run(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed while a fired, covered gap lingers as residue")
	}
	if got := res.GetPruneResidue(); len(got) != 1 || got[0] != gapPath {
		t.Errorf("prune residue = %v, want [%s]", got, gapPath)
	}
	if got := gapState(); got != stipulatorv1.GapState_GAP_STATE_RESOLVED {
		t.Errorf("gap state = %v, want RESOLVED after the condition fired", got)
	}
}

// TestCheckComposesInProcess pins the check's composition rule at the
// import graph: the verdict is assembled from library calls, never from
// subprocess invocations of the individual operations, so this package
// has no business importing subprocess plumbing. Child processes exist
// only behind the backend execution seam, which spawns toolchain
// commands and the owned symbol-resolution child — never a subprocess
// invocation of a stipulator verb.
func TestCheckComposesInProcess(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no production sources parsed — the constraint is vacuous")
	}
	for _, pkg := range pkgs {
		for path, f := range pkg.Files {
			for _, imp := range f.Imports {
				if imp.Path.Value == `"os/exec"` {
					t.Errorf("%s imports os/exec; the check composes library calls, never subprocesses", path)
				}
			}
		}
	}
}

// TestCheckReportsPhaseTransitions pins the pass's phase marks: a run
// with a progress reporter installed reports compile, discovery,
// execution, verification, and coverage in order — the phases a
// long-running check call surfaces as MCP progress and the attribution a
// deadline error names.
func TestCheckReportsPhaseTransitions(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-progress")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(nil))
	var events []*stipulatorv1.ProgressEvent
	rep := progress.New(func(e *stipulatorv1.ProgressEvent) { events = append(events, e) })
	res, err := Run(progress.NewContext(context.Background(), rep), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetPassed() {
		t.Fatalf("fixture check failed: %v", res)
	}
	var phases []stipulatorv1.Phase
	for _, e := range events {
		if len(phases) == 0 || phases[len(phases)-1] != e.GetPhase() {
			phases = append(phases, e.GetPhase())
		}
	}
	want := []stipulatorv1.Phase{
		stipulatorv1.Phase_PHASE_COMPILE,
		stipulatorv1.Phase_PHASE_DISCOVERY,
		stipulatorv1.Phase_PHASE_EXECUTION,
		stipulatorv1.Phase_PHASE_VERIFICATION,
		stipulatorv1.Phase_PHASE_COVERAGE,
	}
	if len(phases) != len(want) {
		t.Fatalf("phase sequence = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phase sequence = %v, want %v", phases, want)
		}
	}
}

func TestCheckCancelledRunYieldsNoVerdict(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict", "REQ-policy-cancellation")
	if testing.Short() {
		t.Skip("executes a policy over a fixture tree")
	}
	neutralAmbient(t)
	t.Run("cancelled before the pass", func(t *testing.T) {
		dir := writeTree(t, baseTree(nil))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := Run(ctx, dir)
		if res != nil {
			t.Errorf("cancelled run returned a verdict: %v", res)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("cancelled before a policy-problem verdict", func(t *testing.T) {
		// The policy-problem path is a pre-execution short circuit; the
		// run's entry guard is what keeps a cancelled run from rendering
		// that verdict.
		files := baseTree(nil)
		delete(files, ".stipulator/policy.textproto")
		dir := writeTree(t, files)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := Run(ctx, dir)
		if res != nil {
			t.Errorf("cancelled run returned a verdict: %v", res)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("cancelled before a compile-problem verdict", func(t *testing.T) {
		// The compile-problems path is the earliest verdict short
		// circuit; a cancelled run must abort before rendering it, too.
		dir := writeTree(t, baseTree(map[string]string{
			"specs/check.md": "# Check\n\n**REQ-fix-dup** (behavior): The fixture MUST pass.\n\n" +
				"**REQ-fix-dup** (behavior): The fixture MUST pass twice.\n",
		}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := Run(ctx, dir)
		if res != nil {
			t.Errorf("cancelled run returned a verdict: %v", res)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
	t.Run("cancelled during execution", func(t *testing.T) {
		dir := writeTree(t, baseTree(map[string]string{
			"slow/slow_test.go": "package slow\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n" +
				"func TestSlow(t *testing.T) {\n\ttime.Sleep(time.Minute)\n}\n",
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		res, err := Run(ctx, dir)
		if res != nil {
			t.Errorf("cancelled run returned a verdict: %v", res)
		}
		if err == nil {
			t.Error("cancelled run returned no error")
		}
	})
}
