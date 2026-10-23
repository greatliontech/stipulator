package golang

import (
	"context"
	"slices"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestNativeRawSelectionIncludesUnsupportedNeighbor(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a raw selector that widens the requested witness subset")
	}
	stipulate.Covers(t, "REQ-go-retained-test-execution", "REQ-evidence-outcome-premises")
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/roster\n\ngo 1.26\n",
		"p_test.go": "package p\nimport (\"os\"; \"testing\"; \"time\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"PWD\") } }\nfunc TestUnsupported(t *testing.T) { if time.Now().IsZero() { t.Fatal(\"clock\") } }\nfunc Example() {\n// Output:\n}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetPlainWitness(true)
	n, err := NormalizeInvocation(t.Context(), dir, goInvocation("native", cfg))
	if err != nil {
		t.Fatal(err)
	}
	// The exported normalized executor also accepts directly supplied runtime
	// arguments. Its native declaration must describe those actual arguments,
	// even when they widen the separate typed TestSelection.
	n.Args = []string{"--test.run=^TestEnv$|^TestUnsupported$|^Example$"}
	if _, err := DiscoverInvocation(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	var final gofresh.FinalizedProcess
	prior := closeNativeProcess
	closeNativeProcess = func(ctx context.Context, unit *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
		result, err := prior(ctx, unit)
		final = result
		return result, err
	}
	t.Cleanup(func() { closeNativeProcess = prior })
	run, err := ExecuteSelection(t.Context(), n, TestSelection{"example.com/roster": {"TestEnv"}})
	if err != nil || findTest(run.Tests, "example.com/roster", "TestEnv").GetOutcome() != passed || findTest(run.Tests, "example.com/roster", "TestUnsupported").GetOutcome() != passed {
		t.Fatalf("actual outcomes: %+v %v", run, err)
	}
	var names []string
	for _, subject := range final.Subjects {
		names = append(names, subject.Subject.Symbol)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Example", "TestEnv", "TestUnsupported"}) || final.OutcomeReason == "" {
		t.Fatalf("actual selector borrowed the typed subset's support: %+v", final)
	}
	for _, result := range final.Subjects {
		if result.Subject.Symbol == "TestEnv" && result.Verdict.Status == gofresh.Valid {
			t.Fatalf("unsupported actual neighbor granted native reuse: %+v", result)
		}
	}
}

func TestNativeRunnableRosterMatchesRetainedExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles one fixture and checks real binary selection semantics")
	}
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/selectors\n\ngo 1.26\n",
		"p_test.go": "package p\nimport (\"flag\"; \"testing\")\nvar custom = flag.String(\"custom\", \"\", \"custom value\")\nfunc TestAlpha(t *testing.T) { t.Run(\"child\", func(t *testing.T){}) }\nfunc TestBeta(t *testing.T) {}\nfunc Example() {\n// Output:\n}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	n, err := NormalizeInvocation(t.Context(), dir, goInvocation("selectors", cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverInvocation(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	const pkg = "example.com/selectors"
	built := buildRetained(t.Context(), n, pkg, retainedProcessHooks{})
	if built.Err != nil {
		t.Fatal(built.Err)
	}
	defer built.Artifact.Close()
	for _, tc := range []struct {
		name, flags     string
		args, selection []string
		refused         bool
	}{
		{name: "raw-widens", args: []string{"--test.run=^(TestBeta|Example)$"}, selection: []string{"TestAlpha"}},
		{name: "subtest-and-example", args: []string{"--test.run=^TestAlpha$/child|^Example$"}},
		{name: "ordered-partial-skip", args: []string{"--test.skip=^TestAlpha$/child|^TestAlpha$"}},
		{name: "positional-stop", args: []string{"operand", "--test.run=TestBeta"}, selection: []string{"TestAlpha"}},
		{name: "two-token-value", args: []string{"--test.outputdir", "--test.run=TestBeta"}, selection: []string{"TestAlpha"}},
		{name: "goflags-run", flags: "-test.run=^TestBeta$"},
		{name: "typed-overrides-goflags", flags: "-test.run=^TestBeta$", selection: []string{"TestAlpha"}},
		{name: "goflags-skip", flags: "-test.skip=TestAlpha", selection: []string{"TestAlpha"}},
		{name: "unknown-arity", args: []string{"--custom", "--test.run=TestBeta"}, selection: []string{"TestAlpha"}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc := *n
			nc.GOFLAGS, nc.Args = tc.flags, tc.args
			environment, err := prepareRetainedEnvironment(&nc, dir, n.GOROOT)
			if err != nil {
				t.Fatal(err)
			}
			defer environment.Close()
			runtime, err := prepareRetainedRuntime(t.Context(), &nc, built.Artifact, retainedRuntimeOptions{Environment: environment, Package: pkg, GOCACHE: n.GOCACHE, Selection: tc.selection}, retainedProcessHooks{})
			if err != nil {
				t.Fatal(err)
			}
			predicted, refusal := nativeRunnableSubjects(nc.runnables[pkg], pkg, runtime.Args)
			if (refusal != nil) != tc.refused {
				t.Fatalf("native roster refusal: %v", refusal)
			}
			actual := runRetained(t.Context(), t.Context(), &nc, runtime, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
			if !actual.HarnessComplete() {
				t.Fatalf("reference did not complete: %+v", actual)
			}
			if tc.refused {
				if len(predicted) != 0 {
					t.Fatal("unknown arity leaked a declared roster")
				}
				return
			}
			var want, got []string
			for _, row := range actual.Stream.tests {
				if row.GetTest() == topLevel(row.GetTest()) {
					want = append(want, row.GetTest())
				}
			}
			for _, subject := range predicted {
				got = append(got, subject.Symbol)
			}
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(got, slices.Compact(want)) {
				t.Fatalf("declared=%v actual=%v args=%v", got, want, runtime.Args)
			}
		})
	}
}
