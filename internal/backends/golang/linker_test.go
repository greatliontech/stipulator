package golang

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	neutralAmbient(t)
	root, external := t.TempDir(), t.TempDir()
	file := filepath.Join(external, "manifest.json")
	if err := os.WriteFile(file, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	c := linkerConfig(t, `linker_strings { package:"example.com/p" variable:"V" value_environment:"LIBRARY_ID" } input_files_from_environment:"MANIFEST"`)
	resolve := func(value, path string) (*NormalizedInvocation, error) {
		env := gotool.SetEnv(gotool.SetEnv(os.Environ(), "LIBRARY_ID", value), "MANIFEST", path)
		n := &NormalizedInvocation{Dir: root, Env: env, WitnessEnv: env}
		err := resolveLinkerSelection(n, c)
		if err == nil {
			err = requireExactInputEligibility(t.Context(), n, root)
		}
		return n, err
	}
	a, err := resolve("ordinary/1:a=b", file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.BracketPaths, []string{file}) {
		t.Fatalf("broadened file bracket: %v", a.BracketPaths)
	}
	if !reflect.DeepEqual(a.LinkerAssignments, []string{"example.com/p.V=ordinary/1:a=b"}) {
		t.Fatal(a.LinkerAssignments)
	}
	b, err := resolve("ordinary/1:b", file)
	if err != nil {
		t.Fatal(err)
	}
	if groupKey(a) == groupKey(b) || groupIdentity(a) != groupIdentity(b) {
		t.Fatal("effective linker value did not split capture while preserving declared coordinate")
	}
	if !slices.Equal(resolvedConfig(a).GetInputFiles(), []string{file}) || !slices.Equal(resolvedConfig(a).GetLinkerAssignments(), a.LinkerAssignments) {
		t.Fatal("resolved report lost selection")
	}
	for _, value := range []string{"", "a b", "a\n", "quote\"", "é"} {
		if _, err := resolve(value, file); err == nil {
			t.Fatalf("admitted value %q", value)
		}
	}
	for _, path := range []string{"", "relative", external, filepath.Join(external, "missing")} {
		if _, err := resolve("value", path); err == nil {
			t.Fatalf("admitted path %q", path)
		}
	}
	link := filepath.Join(external, "link")
	if err := os.Symlink(file, link); err == nil {
		if _, err := resolve("value", link); err == nil {
			t.Fatal("symlink admitted")
		}
	}
	n := &NormalizedInvocation{Dir: root, Env: a.Env, ExcludedPaths: []string{external}}
	if err := resolveLinkerSelection(n, c); err != nil {
		t.Fatal(err)
	}
	if err := requireExactInputEligibility(t.Context(), n, root); err == nil {
		t.Fatal("excluded input admitted")
	}
	n = &NormalizedInvocation{Dir: root, Env: a.Env, ExcludedPaths: []string{string(filepath.Separator)}}
	if err := resolveLinkerSelection(n, c); err != nil {
		t.Fatal(err)
	}
	// Native REQ-inputs-exclusions excludes the root listing alone, not all
	// descendants. A consumer prefix rule must not widen that declaration.
	if err := requireExactInputEligibility(t.Context(), n, root); err != nil {
		t.Fatalf("root-listing exclusion widened: %v", err)
	}
	// Corpus roots may be selected through aliases; VCS exclusions still bind
	// their actual root, not a different lexical prefix.
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	gitFile := filepath.Join(root, ".git", "config")
	if err := os.WriteFile(gitFile, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(external, "root-alias")
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Fatal(err)
	}
	n = &NormalizedInvocation{Dir: rootAlias, Env: gotool.SetEnv(gotool.SetEnv(os.Environ(), "LIBRARY_ID", "value"), "MANIFEST", gitFile)}
	if err := resolveLinkerSelection(n, c); err != nil {
		t.Fatal(err)
	}
	if err := requireExactInputEligibility(t.Context(), n, rootAlias); err == nil {
		t.Fatal("aliased corpus root bypassed VCS exclusion")
	}
	duplicate := linkerConfig(t, `input_files_from_environment:"A" input_files_from_environment:"B"`)
	if err := resolveLinkerSelection(&NormalizedInvocation{Dir: root, Env: []string{"A=" + file, "B=" + file}}, duplicate); err == nil {
		t.Fatal("duplicate file admitted")
	}
	// Use a resolved package directory, so the actual native eligibility
	// boundary, rather than a missing-package refusal, judges the replacement.
	a.Name = "directory-replacement-fixture"
	a.PkgDirs = map[string]string{"example.com/p": root}
	a.PkgClosureDirs = map[string][]string{"example.com/p": {}}
	ready := captureObservationFrame(context.Background(), a, "example.com/p")
	if ready.spawnReason != "" || ready.frame.Reason() != "" || ready.frame.PkgDir != root {
		t.Fatalf("ordinary-file frame was not established: %q %q", ready.spawnReason, ready.frame.Reason())
	}
	// A path replaced by a directory must not widen the bracket at spawn.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	frame := captureObservationFrame(context.Background(), a, "example.com/p")
	if !strings.Contains(frame.spawnReason, "selected input eligibility refused before capture") || !strings.Contains(frame.spawnReason, "MANIFEST") || !strings.Contains(frame.spawnReason, "not raw-byte eligible") {
		t.Fatalf("directory replacement did not refuse: %s", frame.spawnReason)
	}
	// Even a nominally healthy synthetic stream cannot publish a completed
	// observation from this refused frame. This is a refusal fixture, not
	// evidence that a real test process executed.
	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation(a.Name)
	producer.SetProcessId(1)
	producer.SetProcessOrdinal(1)
	observation := observeProcess(context.Background(), a, "example.com/p", producer, &streamState{terminal: "pass"}, nil, stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY, "", frame)
	if observation.Wire.GetCompleted() != nil || observation.Wire.GetIncompleteReason() == "" {
		t.Fatalf("refused frame published an observation: %v", observation.Wire)
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
	c.SetEnvironment(nil)
	if _, err := NormalizeInvocation(t.Context(), discoverFixture(t), goInvocation("test", c)); err == nil {
		t.Fatal("denied linker value resurrected from ambient environment")
	}
}

func TestLinkerExecutionAndFreshnessUseActualAssignments(t *testing.T) {
	if testing.Short() {
		t.Skip("executes and fingerprints a linked fixture")
	}
	stipulate.Covers(t, "REQ-go-linker-selection", "REQ-go-invocation-input-files")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/linked\n\ngo 1.26\n",
		"linked.go": "package linked\nvar Version string\n",
		"linked_test.go": `package linked
import("os";"testing")
func TestValue(t *testing.T) {
 b,err:=os.ReadFile(os.Getenv("MANIFEST")); if err!=nil {t.Fatal(err)}
 if Version!=string(b) { t.Fatalf("actual linked value %q, manifest %q",Version,b) }
}
func TestAbort(t *testing.T) { if os.Getenv("ABORT")=="yes" {os.Exit(3)} }
`,
	})
	file := filepath.Join(t.TempDir(), "manifest")
	t.Setenv("MANIFEST", file)
	var priorGuard, priorCoordinate string
	for _, value := range []string{"one", "two"} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
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
			t.Fatal("environment value orphaned record coordinate")
		}
		priorCoordinate = g.id
		engine, err := groupEngine(t.Context(), dir, g)
		if err != nil {
			t.Fatal(err)
		}
		view, err := engine.NewView(t.Context(), groupSubjects(g), dir)
		if err != nil {
			t.Fatal(err)
		}
		fps, err := view.CaptureBatch(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		// Literal independent flag input detects omission from the engine path.
		control, err := newEngine(t.Context(), dir, g.witnessEnv, []string{"-ldflags=-X example.com/linked.Version=" + value}, gofresh.WithEvidenceRoot(dir))
		if err != nil {
			t.Fatal(err)
		}
		cv, err := control.NewView(t.Context(), groupSubjects(g), dir)
		if err != nil {
			t.Fatal(err)
		}
		want, err := cv.CaptureBatch(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fp := fps[gofresh.Subject{Package: "example.com/linked", Symbol: "TestValue"}]
		if fp.Guards.BuildConfig == "" || fp.Guards.BuildConfig != want[gofresh.Subject{Package: "example.com/linked", Symbol: "TestValue"}].Guards.BuildConfig || fp.Guards.BuildConfig == priorGuard {
			t.Fatal("link assignment absent or divergent in freshness guard")
		}
		priorGuard = fp.Guards.BuildConfig
		health, tests, _, observations, err := ExecuteInvocation(t.Context(), n, discovery.invocations[0].obligations)
		if err != nil {
			t.Fatal(err)
		}
		if health.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY || findTest(tests, "example.com/linked", "TestValue").GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
			t.Fatalf("actual linked execution failed: %v %v", health, tests)
		}
		if len(observations) != 1 || observations[0].Wire.GetCompleted() == nil {
			t.Fatalf("missing completed file observation: %v", observations)
		}
		manifest := observations[0].Wire.GetCompleted().GetManifest()
		current := func() runtimeinput.State {
			t.Helper()
			s, err := runtimeinput.Current(t.Context(), manifest, dir, witnessEnvOf(n))
			if err != nil || s.Unverifiable || !s.OK {
				t.Fatalf("exact file observation refused: %+v %v", s, err)
			}
			return s
		}
		before := current()
		if before.Digest != observations[0].Wire.GetCompleted().GetDigest() {
			t.Fatal("unchanged observed file failed revalidation")
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(file), "unrelated"), []byte("sibling"), 0600); err != nil {
			t.Fatal(err)
		}
		if current().Digest != before.Digest {
			t.Fatal("sibling outside exact input changed its observation")
		}
		if err := os.WriteFile(file, []byte("changed-manifest"), 0600); err != nil {
			t.Fatal(err)
		}
		if current().Digest == before.Digest {
			t.Fatal("changed manifest bytes retained observed identity")
		}
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		// Independent wrong-value control must fail, not merely render a flag.
		bad := *n
		bad.LinkerAssignments = []string{"example.com/linked.Version=wrong"}
		result, err := ExecuteSelection(t.Context(), &bad, TestSelection{"example.com/linked": {"TestValue"}})
		if err != nil {
			t.Fatal(err)
		}
		if findTest(result.Tests, "example.com/linked", "TestValue").GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
			t.Fatal("wrong linker value did not reach real execution")
		}
		// Force the actual isolation path: the sibling abort denies the first
		// process's passing outcome, so TestValue must pass in its own process.
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
			if process.Test == "TestValue" && process.Disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
				found = true
			}
		}
		if !found {
			t.Fatalf("linked isolated rerun did not pass: %+v", retry.Processes)
		}
	}
}
