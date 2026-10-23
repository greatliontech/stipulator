package golang

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
)

func linkerConfig(t *testing.T, text string) *stipulatorv1.GoInvocationConfig {
	t.Helper()
	c := &stipulatorv1.GoInvocationConfig{}
	if err := prototext.Unmarshal([]byte(text), c); err != nil {
		t.Fatal(err)
	}
	return c
}

// Exact-file positive fixtures use physical coordinates even when the host's
// temporary root is a symlink. Alias refusal is tested independently.
func linkerTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLinkerSelectionClosedPolicy(t *testing.T) {
	stipulate.Covers(t, "REQ-go-linker-selection", "REQ-go-invocation-input-files")
	for _, text := range []string{
		`linker_strings { package:"example.com/p" variable:"Version" value:"" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value_environment:"LIBRARY_ID" }`,
		`input_files_from_environment:"MANIFEST"`,
	} {
		if err := validateConfig(linkerConfig(t, text)); err != nil {
			t.Fatalf("valid %s: %v", text, err)
		}
	}
	for _, text := range []string{
		`linker_strings { package:"example.com/p" variable:"Version" }`,
		`linker_strings { package:"example.com/p" variable:"not-a-var" value:"1" }`,
		`linker_strings { package:"../p" variable:"Version" value:"1" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value:"a -X other.v=b" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value:"\"quoted\"" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value:"back\\slash" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value:"\000" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value_environment:"BAD=ENV" }`,
		`linker_strings { package:"example.com/p" variable:"Version" value:"1" } linker_strings { package:"example.com/p" variable:"Version" value:"2" }`,
		`linker_strings { package:"example.com/z" variable:"V" value:"1" } linker_strings { package:"example.com/a" variable:"V" value:"2" }`,
		`input_files_from_environment:"Z" input_files_from_environment:"A"`,
		`input_files_from_environment:"A" input_files_from_environment:"A"`,
		`input_files_from_environment:"1INVALID"`,
		`goflags:"-ldflags=-X=example.com/p.V=1"`,
		`environment:"GOFLAGS=-ldflags=-X=example.com/p.V=1"`,
	} {
		if err := validateConfig(linkerConfig(t, text)); err == nil {
			t.Fatalf("admitted %s", text)
		}
	}
}

func TestLinkerResolutionFreezesValuesAndExactFiles(t *testing.T) {
	stipulate.Covers(t, "REQ-go-linker-selection", "REQ-go-invocation-input-files")
	file := filepath.Join(linkerTempDir(t), "manifest")
	c := linkerConfig(t, `linker_strings { package:"example.com/p" variable:"V" value_environment:"LIBRARY_ID" } input_files_from_environment:"MANIFEST"`)
	resolve := func(value, path string) (*NormalizedInvocation, error) {
		n := &NormalizedInvocation{Env: []string{"LIBRARY_ID=" + value, "MANIFEST=" + path}, PkgClosureDirs: map[string][]string{"p": {"."}}}
		return n, resolveLinkerSelection(n, c)
	}
	a, err := resolve("ordinary/1:a=b", file)
	if err != nil {
		t.Fatal(err)
	}
	brackets, why := bracketRootsFor(a, "p")
	if why != "" || !slices.Equal(brackets, []string{".", file}) {
		t.Fatalf("broadened file bracket: %v %s", brackets, why)
	}
	if !slices.Equal(a.LinkerAssignments, []string{"example.com/p.V=ordinary/1:a=b"}) {
		t.Fatal(a.LinkerAssignments)
	}
	b, err := resolve("ordinary/1:b", file)
	if err != nil {
		t.Fatal(err)
	}
	if groupKey(a) == groupKey(b) || groupIdentity(a) != groupIdentity(b) {
		t.Fatal("effective value did not split capture while preserving declared coordinate")
	}
	b, err = resolve("ordinary/1:a=b", file+"2")
	if err != nil {
		t.Fatal(err)
	}
	if groupKey(a) == groupKey(b) || groupIdentity(a) != groupIdentity(b) {
		t.Fatal("effective path did not split capture while preserving declared coordinate")
	}
	literal := &NormalizedInvocation{Env: a.Env}
	if err := resolveLinkerSelection(literal, linkerConfig(t, `linker_strings { package:"example.com/p" variable:"V" value:"ordinary/1:a=b" } input_files_from_environment:"MANIFEST"`)); err != nil {
		t.Fatal(err)
	}
	if groupIdentity(a) == groupIdentity(literal) || groupKey(a) == groupKey(literal) {
		t.Fatal("literal and environment declarations shared identity")
	}
	if !slices.Equal(resolvedConfig(a).GetInputFiles(), []string{file}) || !slices.Equal(resolvedConfig(a).GetLinkerAssignments(), a.LinkerAssignments) {
		t.Fatal("resolved report lost selection")
	}
	for _, value := range []string{"", "a b", "a\n", "quote\"", "é"} {
		if _, err := resolve(value, file); err == nil {
			t.Fatalf("admitted value %q", value)
		}
	}
	for _, path := range []string{"", "relative", filepath.Dir(file) + "/../manifest"} {
		if _, err := resolve("value", path); err == nil {
			t.Fatalf("admitted path %q", path)
		}
	}
	if err := resolveLinkerSelection(&NormalizedInvocation{Env: []string{"A=" + file, "B=" + file}}, linkerConfig(t, `input_files_from_environment:"A" input_files_from_environment:"B"`)); err == nil {
		t.Fatal("duplicate file admitted")
	}
	empty := &NormalizedInvocation{}
	if err := resolveLinkerSelection(empty, linkerConfig(t, `linker_strings { package:"example.com/p" variable:"V" value:"" }`)); err != nil || !slices.Equal(empty.LinkerAssignments, []string{"example.com/p.V="}) {
		t.Fatalf("explicit empty literal lost: %v %v", empty.LinkerAssignments, err)
	}
}

func TestLinkerAbsentSelectionKeepsExistingCoordinate(t *testing.T) {
	n := &NormalizedInvocation{WitnessEnv: []string{"K=V"}}
	want := "tags=\x00race=false\x00goos=\"\"\x00goarch=\"\"\x00cgo=\"\"\x00goflags=\"\"\x00workspace=false\x00modulemode=\"GO_MODULE_MODE_UNSPECIFIED\"\x00module_root=\"\"\x00pgo=\"\"\x00toolchain=\"\"\x00args=\x00envset=\x00envdeny="
	if got := groupIdentity(n); got != want {
		t.Fatalf("pre-feature identity moved:\n%q\nwant %q", got, want)
	}
	if err := resolveLinkerSelection(n, &stipulatorv1.GoInvocationConfig{}); err != nil {
		t.Fatal(err)
	}
	if groupIdentity(n) != want || strings.Contains(groupKey(n), "linker_") || strings.Contains(groupKey(n), "input_file") {
		t.Fatal("absent features added coordinates")
	}
	n.Args = []string{"-ldflags=-X example.com/p.V=1"}
	if strings.Contains(strings.Join(retainedBuildArgs(n, []string{"p"}, "/artifact", false), " "), "ldflags") {
		t.Fatal("binary args became a compilation flag channel")
	}
}

func TestLinkerNormalizedOverrideAndDenial(t *testing.T) {
	stipulate.Covers(t, "REQ-go-linker-selection")
	neutralAmbient(t)
	t.Setenv("LIBRARY_ID", "ambient")
	c := linkerConfig(t, `packages:"./..." linker_strings { package:"example.com/p" variable:"V" value_environment:"LIBRARY_ID" } env_deny:"LIBRARY_ID" environment:"LIBRARY_ID=reviewed"`)
	n, err := NormalizeInvocation(t.Context(), discoverFixture(t), goInvocation("test", c))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.LinkerAssignments, []string{"example.com/p.V=reviewed"}) {
		t.Fatal(n.LinkerAssignments)
	}
	if n.WitnessEnv != nil || n.SpawnBound != 0 {
		t.Fatal("linker normalization finalized allocation early")
	}
	c.SetEnvironment(nil)
	if _, err := NormalizeInvocation(t.Context(), discoverFixture(t), goInvocation("test", c)); err == nil {
		t.Fatal("denied linker value resurrected")
	}
}

func TestLinkerExecutionAndFreshnessUseActualAssignments(t *testing.T) {
	if testing.Short() {
		t.Skip("executes and fingerprints linked receipt fixtures")
	}
	stipulate.Covers(t, "REQ-go-linker-selection", "REQ-go-invocation-input-files")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/linked\n\ngo 1.26\n",
		"linked.go": "package linked\nvar Version string\n",
		"linked_test.go": `package linked
import("crypto/sha256";"fmt";"os";"testing")
func TestValue(t *testing.T) {
 b,err:=os.ReadFile(os.Getenv("MANIFEST")); if err!=nil {t.Fatal(err)}
 want:=fmt.Sprintf("ordinary/1:%x",sha256.Sum256(b))
 if Version!=want { t.Fatalf("actual linked receipt %q, manifest digest %q",Version,want) }
}
func TestNoRead(t *testing.T) {}
func TestAbort(t *testing.T) { if os.Getenv("ABORT")=="yes" {os.Exit(3)} }
`,
	})
	file := filepath.Join(linkerTempDir(t), "manifest")
	t.Setenv("MANIFEST", file)
	var priorGuard, priorCoordinate string
	for _, bytes := range []string{"one\n", "two\n"} {
		if err := os.WriteFile(file, []byte(bytes), 0600); err != nil {
			t.Fatal(err)
		}
		value := fmt.Sprintf("ordinary/1:%x", sha256.Sum256([]byte(bytes)))
		t.Setenv("LIBRARY_ID", value)
		c := linkerConfig(t, `packages:"./..." plain_witness:true linker_strings { package:"example.com/linked" variable:"Version" value_environment:"LIBRARY_ID" } input_files_from_environment:"MANIFEST"`)
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("linked", c)})
		capture, err := CapturePolicy(t.Context(), dir, p)
		if err != nil {
			t.Fatal(err)
		}
		discovery, err := capture.discover(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		n, g := capture.normalized[0], discovery.groups[0]
		if priorCoordinate != "" && priorCoordinate != g.id {
			t.Fatal("environment value orphaned coordinate")
		}
		priorCoordinate = g.id
		snapshot, inputs, err := groupSnapshot(t.Context(), dir, g)
		if err != nil {
			t.Fatal(err)
		}
		leg, err := nativePackageLeg(t.Context(), snapshot, inputs, n, "example.com/linked")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(leg.dispose)
		subject := gofresh.Subject{Package: "example.com/linked", Symbol: "TestValue"}
		fp, err := leg.binding.CurrentFacts(t.Context(), subject)
		if err != nil {
			t.Fatal(err)
		}
		// Independent literal flags catch omission from the native build owner.
		control, err := gofresh.NewSession(t.Context(), gofresh.BuildDescription{Directory: dir, Environment: g.witnessEnv, Flags: []string{"-ldflags=-X example.com/linked.Version=" + value}, Inputs: []string{}}, gofresh.SessionPolicy{EvidenceRoot: dir, Execution: gofresh.PackageExecution, Runner: engineRunner})
		if err != nil {
			t.Fatal(err)
		}
		cs, err := control.Snapshot(t.Context(), groupSubjects(g), gofresh.CodeResult)
		if err != nil {
			t.Fatal(err)
		}
		cb, err := cs.BindRuntime(t.Context(), gofresh.RuntimeDescription{Directory: leg.environment.Dir, Environment: leg.environment.Env})
		if err != nil {
			t.Fatal(err)
		}
		want, err := cb.CurrentFacts(t.Context(), subject)
		if err != nil {
			t.Fatal(err)
		}
		if fp.Guards.BuildConfig == "" || fp.Guards.BuildConfig != want.Guards.BuildConfig || fp.Guards.BuildConfig == priorGuard {
			t.Fatal("native build guard lost or changed linker selection")
		}
		priorGuard = fp.Guards.BuildConfig
		// Execution-only capture has a separate group constructor. It must carry
		// the same flags even when no selective witness capture was requested.
		executionViews := executionLegs(t.Context(), n, []string{"example.com/linked"})
		executionView := executionViews["example.com/linked"]
		if executionView == nil {
			t.Fatal("execution-only native preparation unavailable")
		}
		executionFP, err := executionView.binding.CurrentFacts(t.Context(), subject)
		executionView.dispose()
		if err != nil || executionFP.Guards.BuildConfig != want.Guards.BuildConfig {
			t.Fatalf("execution-only constructor lost linker guard: %v", err)
		}
		// Warm checking sees prospective retained coverage preparation, not a
		// reconstructed witness environment whose old GOCOVERDIR still agrees.
		coverage := *n
		coverage.Env = gotool.SetEnv(n.Env, "GOCOVERDIR", file)
		coverage.WitnessEnv = gotool.SetEnv(n.WitnessEnv, "GOCOVERDIR", file)
		coverage.InputFiles = []selectedInputFile{{Name: "GOCOVERDIR", Path: file}}
		retainedSetFlags(&coverage, "-cover")
		if refused, err := nativePackageLeg(t.Context(), snapshot, inputs, &coverage, "example.com/linked"); err == nil || !strings.Contains(err.Error(), "no longer selects") {
			if refused != nil {
				refused.dispose()
			}
			t.Fatalf("warm runtime admitted rewritten selector: %v", err)
		}
		health, tests, _, observations, err := ExecuteInvocation(t.Context(), n, discovery.invocations[0].obligations)
		if err != nil {
			t.Fatal(err)
		}
		if health.GetDisposition() != healthy || findTest(tests, "example.com/linked", "TestValue").GetOutcome() != passed {
			t.Fatalf("linked receipt failed: %v %v", health, tests)
		}
		if !slices.Equal(health.GetGo().GetLinkerAssignments(), n.LinkerAssignments) {
			t.Fatal("execution report omitted assignments")
		}
		if len(observations) != 1 || observations[0].Wire.GetCompleted() == nil {
			t.Fatalf("missing completed observation: %v", observations)
		}
		obs := observations[0]
		current := func() runtimeinput.State {
			t.Helper()
			s, err := runtimeinput.Current(t.Context(), obs.Wire.GetCompleted().GetManifest(), dir, obs.runtime.Environment)
			if err != nil || s.Unverifiable || !s.OK {
				t.Fatalf("exact observation refused: %+v %v", s, err)
			}
			return s
		}
		before := current()
		if before.Digest != obs.Wire.GetCompleted().GetDigest() {
			t.Fatal("unchanged file failed revalidation")
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(file), "sibling"), []byte("unrelated"), 0600); err != nil {
			t.Fatal(err)
		}
		if current().Digest != before.Digest {
			t.Fatal("exact bracket admitted sibling")
		}
		if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		if current().Digest == before.Digest {
			t.Fatal("manifest change retained observed identity")
		}
		if err := os.WriteFile(file, []byte(bytes), 0600); err != nil {
			t.Fatal(err)
		}
		for _, assignments := range [][]string{nil, {"example.com/linked.Version=wrong"}} {
			bad := *n
			bad.LinkerAssignments = assignments
			result, err := ExecuteSelection(t.Context(), &bad, TestSelection{"example.com/linked": {"TestValue"}})
			if err != nil {
				t.Fatal(err)
			}
			if findTest(result.Tests, "example.com/linked", "TestValue").GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
				t.Fatal("missing/distinct linker identity did not fail actual receipt")
			}
		}
		// An unrelated selected test need not read the declared file. Its native
		// account must not manufacture the file dependency from the declaration.
		noRead, err := ExecuteSelection(t.Context(), n, TestSelection{"example.com/linked": {"TestNoRead"}})
		if err != nil {
			t.Fatal(err)
		}
		if findTest(noRead.Tests, "example.com/linked", "TestNoRead").GetOutcome() != passed || len(noRead.Observations) != 1 {
			t.Fatalf("no-read execution: %+v", noRead)
		}
		if noRead.Observations[0].Wire.GetCompleted() == nil {
			t.Fatalf("no-read process lacks completed native account: %v", noRead.Observations[0].Wire)
		}
		paths, err := runtimeinput.Paths(noRead.Observations[0].Wire.GetCompleted().GetManifest(), dir)
		if err != nil || slices.Contains(paths, file) {
			t.Fatalf("declaration invented file observation: %v %v", paths, err)
		}
		c.SetEnvironment([]string{"ABORT=yes"})
		isolated, err := NormalizeInvocation(t.Context(), dir, goInvocation("linked", c))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DiscoverInvocation(t.Context(), isolated); err != nil {
			t.Fatal(err)
		}
		retry, err := ExecuteSelection(t.Context(), isolated, TestSelection{"example.com/linked": {"TestValue", "TestAbort"}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, process := range retry.Processes {
			if process.Test == "TestValue" && process.Disposition == healthy {
				found = true
			}
		}
		if !found {
			t.Fatalf("linked isolation retry did not pass: %+v", retry.Processes)
		}
	}
}

// Keep the frozen runtime environment explicit in eligibility-only fixtures.
func linkerRuntime(t *testing.T, n *NormalizedInvocation) *retainedEnvironment {
	t.Helper()
	env, err := gotool.EnvForCommand(n.Env, n.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return &retainedEnvironment{Dir: n.Dir, Env: env}
}
