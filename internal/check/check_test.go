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
	"github.com/greatliontech/stipulator/internal/compile"
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
	// The witness store lives under the user cache directory; tests must
	// never touch the real one.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	res, err := Run(context.Background(), dir, true, nil)
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
	gb, err := golang.NewOwned(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer gb.Close()
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

	res, err := Run(ctx, dir, true, nil)
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
	res, err = Run(ctx, dir, true, nil)
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
	gb, err := golang.NewOwned(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer gb.Close()
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

	res, err := Run(ctx, dir, true, nil)
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
	res, err = Run(ctx, dir, true, nil)
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
	res, err := Run(progress.NewContext(context.Background(), rep), dir, true, nil)
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
		res, err := Run(ctx, dir, true, nil)
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
		res, err := Run(ctx, dir, true, nil)
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
		res, err := Run(ctx, dir, true, nil)
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
		res, err := Run(ctx, dir, true, nil)
		if res != nil {
			t.Errorf("cancelled run returned a verdict: %v", res)
		}
		if err == nil {
			t.Error("cancelled run returned no error")
		}
	})
}

// A policy whose witness-eligible selection covers no expected witness -
// every invocation non-race - names its execution-layer cause once at
// result level and classes each affected binding's reason, instead of
// reporting every binding as an unexplained tree defect
// (REQ-check-witness-selection).
func TestCheckNamesAnEmptyWitnessSelection(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("runs the witness pass over a temporary corpus")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md": "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
	}))
	// The binding is fully pinned (content and shape) so the row's ONLY
	// red cause is the selection boundary — the policy-blocked class
	// asserted below is defined for exactly that purity.
	spec, _, err := compile.Compile(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	gb, err := golang.NewOwned(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	resolution, shape, err := gb.Resolve("example.com/checkfix/ok.TestDouble")
	gb.Close()
	if err != nil || resolution != verify.Resolved {
		t.Fatalf("resolving the fixture test: %v %v", resolution, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".stipulator", "bindings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".stipulator", "bindings", "bound.textproto"), []byte("bindings {\n"+
		"  requirement_id: \"REQ-fix-bound\"\n"+
		"  content_hash: \""+spec.GetRequirements()[0].GetContentHash()+"\"\n"+
		"  backend: \"go\"\n"+
		"  symbol: \"example.com/checkfix/ok.TestDouble\"\n"+
		"  role: BINDING_ROLE_TESTS\n"+
		"  shape_hash: \""+shape+"\"\n"+
		"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetPassed() {
		t.Error("check passed with an unwitnessable binding")
	}
	if res.GetTestsExecuted() != 0 || res.GetTestsServed() != 0 {
		t.Fatalf("executed=%d served=%d, want the zero-witness shape", res.GetTestsExecuted(), res.GetTestsServed())
	}
	if res.GetTestsOutsidePolicy() == 0 {
		t.Fatal("outside-selection count missing from the result")
	}
	if p := res.GetWitnessSelectionProblem(); !strings.Contains(p, "covered no expected witness") || !strings.Contains(p, "race") {
		t.Fatalf("witness selection problem = %q, want the execution-layer cause named", p)
	}
	reason := ""
	blocked := false
	for _, row := range res.GetCoverage().GetRequirements() {
		if row.GetId() == "REQ-fix-bound" {
			reason = strings.Join(row.GetReasons(), "; ")
			blocked = row.GetWitnessSelectionBlocked()
		}
	}
	if !strings.Contains(reason, "outside the policy's witness-eligible selection") || !strings.Contains(reason, "race: true") {
		t.Fatalf("binding reason = %q, want the selection class named per binding", reason)
	}
	// The row is red solely on the selection boundary, so it carries the
	// policy-blocked class that lets bounded projections fold it behind
	// the result-level diagnostic (REQ-check-witness-selection).
	if !blocked {
		t.Fatal("selection-boundary red not classed policy-blocked")
	}
}

// With a race invocation covering the package, the same corpus witnesses
// and the selection problem stays absent - the diagnostic never fires on a
// healthy selection (REQ-check-witness-selection).
func TestCheckWitnessSelectionProblemAbsentUnderRacePolicy(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("runs the witness pass over a temporary corpus")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/policy.textproto": racePolicy,
		"specs/check.md":               "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
		".stipulator/bindings/bound.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-bound\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	res, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetWitnessSelectionProblem() != "" {
		t.Fatalf("selection problem fired on a healthy selection: %q", res.GetWitnessSelectionProblem())
	}
	if res.GetTestsOutsidePolicy() != 0 {
		t.Fatalf("outside=%d on a fully covered selection", res.GetTestsOutsidePolicy())
	}
	if res.GetTestsExecuted()+res.GetTestsServed() == 0 {
		t.Fatal("nothing witnessed under the race policy")
	}
}

// The witness-eligible selection boundary holds on the health-judged form:
// a race-uncovered subject counts and classes identically, and the healthy
// all-non-race tree fires the result-level cause - non-race legs execute
// but can never grant (REQ-check-witness-selection's form-neutral arm).
func TestCheckFullFormNamesRaceUncoveredSubjects(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("executes the whole policy over a temporary corpus")
	}
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		"specs/check.md": "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
		".stipulator/bindings/bound.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-bound\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	res, err := Run(context.Background(), dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetTestsOutsidePolicy() == 0 {
		t.Fatal("full form dropped the outside-selection count")
	}
	if p := res.GetWitnessSelectionProblem(); !strings.Contains(p, "race") {
		t.Fatalf("full form selection problem = %q", p)
	}
	reason := ""
	for _, row := range res.GetCoverage().GetRequirements() {
		if row.GetId() == "REQ-fix-bound" {
			reason = strings.Join(row.GetReasons(), "; ")
		}
	}
	if !strings.Contains(reason, "outside the policy's witness-eligible selection") {
		t.Fatalf("full-form binding reason = %q, want the selection class", reason)
	}
}

// A subject selected only by multiple non-race invocations executes -
// failures would count - but can never witness: it is outside the eligible
// selection, and the executing legs do not mask the result-level cause
// (REQ-check-witness-selection; the multiply-non-race shape).
func TestCheckMultiplyNonRaceSelectedSubjectsAreOutside(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("runs the witness pass over a temporary corpus")
	}
	neutralAmbient(t)
	twoPlain := "invocations {\n  name: \"a\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./ok\"\n  }\n}\ninvocations {\n  name: \"b\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./ok\"\n  }\n}\n"
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/policy.textproto": twoPlain,
		"specs/check.md":               "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
		".stipulator/bindings/bound.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-bound\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	res, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.GetTestsOutsidePolicy() == 0 {
		t.Fatal("multiply-non-race-selected subject not counted outside")
	}
	if p := res.GetWitnessSelectionProblem(); !strings.Contains(p, "covered no expected witness") {
		t.Fatalf("executing non-race legs masked the cause: %q", p)
	}
	reason := ""
	for _, row := range res.GetCoverage().GetRequirements() {
		if row.GetId() == "REQ-fix-bound" {
			reason = strings.Join(row.GetReasons(), "; ")
		}
	}
	if !strings.Contains(reason, "outside the policy's witness-eligible selection") {
		t.Fatalf("multiply-non-race binding reason = %q", reason)
	}
}

// A policy admitting a non-race invocation at the plain tier grants
// witness evidence from it: the check witnesses, the selection problem
// stays absent, and the granted witness records the downgrade — its
// race attribute reads false (REQ-check-witness-selection).
func TestCheckPlainWitnessAdmissionGrantsPlainTierWitnesses(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("runs the witness pass over a temporary corpus")
	}
	neutralAmbient(t)
	plainWitnessPolicy := "invocations {\n  name: \"plain\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    plain_witness: true\n  }\n}\n"
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/policy.textproto": plainWitnessPolicy,
		"specs/check.md":               "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
		".stipulator/bindings/bound.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-bound\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	res, err := Run(context.Background(), dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := res.GetWitnessSelectionProblem(); p != "" {
		t.Fatalf("selection problem fired under an admitted plain invocation: %q", p)
	}
	if res.GetTestsOutsidePolicy() != 0 {
		t.Fatalf("outside-selection count = %d under an admitted plain invocation", res.GetTestsOutsidePolicy())
	}
	var bound *stipulatorv1.BindingResult
	for _, r := range res.GetVerify().GetResults() {
		if r.GetSymbol() == "example.com/checkfix/ok.TestDouble" {
			bound = r
		}
	}
	if bound == nil || bound.GetTestOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Fatalf("bound test outcome = %+v, want a granted pass", bound)
	}
	// The downgrade is recorded: a plain-tier witness never claims race
	// rigor.
	if bound.GetRaceEnabled() {
		t.Fatal("plain-tier witness claims race rigor")
	}

	// The full (health-judged) form grants and classes identically.
	full, err := Run(context.Background(), dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var fullBound *stipulatorv1.BindingResult
	for _, r := range full.GetVerify().GetResults() {
		if r.GetSymbol() == "example.com/checkfix/ok.TestDouble" {
			fullBound = r
		}
	}
	if fullBound == nil || fullBound.GetTestOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED || fullBound.GetRaceEnabled() {
		t.Fatalf("health-judged form's plain witness = %+v, want granted pass without race rigor", fullBound)
	}
	// The boundary holds identically on the health-judged form: an
	// admitted plain invocation's subjects are inside the selection, so
	// neither the count nor the diagnostic fires
	// (REQ-check-witness-selection's both-forms sentence).
	if full.GetTestsOutsidePolicy() != 0 || full.GetWitnessSelectionProblem() != "" {
		t.Fatalf("health-judged form classes admitted subjects outside: outside=%d problem=%q", full.GetTestsOutsidePolicy(), full.GetWitnessSelectionProblem())
	}
	for _, row := range full.GetCoverage().GetRequirements() {
		if row.GetId() == "REQ-fix-bound" && row.GetWitnessSelectionBlocked() {
			t.Fatal("health-judged form classed an admitted subject's requirement policy-blocked")
		}
	}
}

// A key granted by any race leg holds the race tier: a multiply-selected
// package covered by one race and one plain-admitted invocation grants
// under both legs, and the witness records race rigor — the downgrade
// marks only keys granted exclusively at the plain tier
// (REQ-check-witness-selection).
func TestCheckRaceLegPrecedenceOverPlainAdmission(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection")
	if testing.Short() {
		t.Skip("runs the witness pass over a temporary corpus")
	}
	neutralAmbient(t)
	mixedPolicy := "invocations {\n  name: \"plain\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    plain_witness: true\n  }\n}\n" +
		"invocations {\n  name: \"race\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n"
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/policy.textproto": mixedPolicy,
		"specs/check.md":               "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
		".stipulator/bindings/bound.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-bound\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	for _, full := range []bool{false, true} {
		res, err := Run(context.Background(), dir, full, nil)
		if err != nil {
			t.Fatal(err)
		}
		var bound *stipulatorv1.BindingResult
		for _, r := range res.GetVerify().GetResults() {
			if r.GetSymbol() == "example.com/checkfix/ok.TestDouble" {
				bound = r
			}
		}
		if bound == nil || bound.GetTestOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
			t.Fatalf("full=%v: bound outcome = %+v, want a granted pass", full, bound)
		}
		if !bound.GetRaceEnabled() {
			t.Fatalf("full=%v: race-legged grant downgraded to the plain tier", full)
		}
	}
}

// Records produced at one witness tier never serve the other: the race
// flag is a caller-supplied build input of every fingerprint
// (REQ-evidence-witness-freshness), so flipping an invocation between
// plain_witness and race re-executes instead of laundering the tier —
// in either direction (REQ-check-witness-selection's auditable-downgrade
// sentence).
func TestCheckTierFlipNeverServesCrossTier(t *testing.T) {
	stipulate.Covers(t, "REQ-check-witness-selection", "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("runs the witness pass over a temporary corpus")
	}
	neutralAmbient(t)
	plainWitnessPolicy := "invocations {\n  name: \"suite\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    plain_witness: true\n  }\n}\n"
	raceSuitePolicy := "invocations {\n  name: \"suite\"\n  timeout {\n    seconds: 300\n  }\n  go {\n    packages: \"./...\"\n    race: true\n  }\n}\n"
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/policy.textproto": plainWitnessPolicy,
		"specs/check.md":               "# Check\n\n**REQ-fix-bound** (behavior): The fixture MUST double.\n",
		".stipulator/bindings/bound.textproto": "bindings {\n" +
			"  requirement_id: \"REQ-fix-bound\"\n" +
			"  backend: \"go\"\n" +
			"  symbol: \"example.com/checkfix/ok.TestDouble\"\n" +
			"  role: BINDING_ROLE_TESTS\n" +
			"}\n",
	}))
	run := func(wantServed bool, wantRace bool) {
		t.Helper()
		res, err := Run(context.Background(), dir, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if served := res.GetTestsServed() > 0; served != wantServed {
			t.Fatalf("served=%v (executed %d), want served=%v", served, res.GetTestsExecuted(), wantServed)
		}
		for _, r := range res.GetVerify().GetResults() {
			if r.GetSymbol() == "example.com/checkfix/ok.TestDouble" {
				if r.GetTestOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED || r.GetRaceEnabled() != wantRace {
					t.Fatalf("witness = outcome %v race %v, want passed race=%v", r.GetTestOutcome(), r.GetRaceEnabled(), wantRace)
				}
			}
		}
	}
	writePolicy := func(p string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".stipulator", "policy.textproto"), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(false, false) // plain: executes, publishes plain-tier records
	run(true, false)  // plain again: serves its own tier
	writePolicy(raceSuitePolicy)
	run(false, true) // flip to race: the plain records must NOT serve
	writePolicy(plainWitnessPolicy)
	// Flip back: the ORIGINAL plain-tier record is still an installed
	// variant and legitimately serves — at its own tier, race rigor
	// unclaimed. The race-produced record cannot serve here (its
	// fingerprint pins the race build input), so a served pass with
	// race_enabled=false is the only sound outcome.
	run(true, false)
}
