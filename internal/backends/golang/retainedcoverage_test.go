package golang

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedCoverageBuildOnlyAndUnexecutedImports(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"testless/p.go": "package testless\nfunc Uncalled() int { return 3 }\n",
		"dep/p.go":      "package dep\nfunc Called() int { return 42 }\nfunc Uncalled() int { return 7 }\n",
		"sub/p.go":      "package sub\nimport \"retained.test/dep\"\nfunc Answer() int { return dep.Called() }\n",
		"sub/p_test.go": "package sub\nimport \"testing\"\nfunc TestAnswer(t *testing.T) {if Answer()!=42 {t.Fatal(\"wrong\")}}\n",
	})
	retainedSetFlags(n, "-coverpkg=./... -coverprofile=coverage.txt")
	profile, err := openRetainedCoverage(n)
	if err != nil {
		t.Fatal(err)
	}
	var artifacts []*retainedArtifact
	scope := buildRetainedScope(t.Context(), n, []string{"retained.test/testless", "retained.test/sub"}, retainedProcessHooks{})
	defer scope.Close()
	if scope.Err != nil {
		t.Fatalf("scope compile: %v; stream=%+v", scope.Err, scope.Stream)
	}
	for _, pkg := range []string{"retained.test/testless", "retained.test/sub"} {
		r := scope.Packages[pkg]
		if r.Err != nil {
			t.Fatalf("build=%+v stream=%+v", r, r.Stream)
		}
		if r.Artifact != nil {
			defer r.Artifact.Close()
			artifacts = append(artifacts, r.Artifact)
		}
		if strings.HasSuffix(pkg, "/testless") {
			if !r.NoTestFiles {
				t.Fatalf("covered testless package acquired a runtime: %+v stream=%+v", r, r.Stream)
			}
			if err := mergeRetainedBuildCoverage(t.Context(), n, r.Artifact, profile, retainedProcessHooks{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime := retainedRuntimeFor(t, n, artifacts[1], "retained.test/sub", nil, profile, artifacts...)
	result := runRetained(t.Context(), t.Context(), n, runtime, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() {
		t.Fatalf("coverage result=%+v stream=%+v", result, result.Stream)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(n.Dir, "coverage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// One direct Go invocation is the oracle for merged package fragments.
	args := []string{"test", "-json", "-timeout=0", "-count=1", "retained.test/testless", "retained.test/sub"}
	if out, err := ownedRunner.Run(t.Context(), n.Dir, witnessEnvOf(n), args...); err != nil {
		t.Fatalf("oracle: %v; %s", err, out)
	}
	want, err := os.ReadFile(filepath.Join(n.Dir, "coverage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(b []byte) string {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	if canonical(got) != canonical(want) {
		t.Fatalf("merged coverage differs\nretained:\n%s\ngo:\n%s", got, want)
	}
}

func TestRetainedCoverageMergerRejectsMalformedFragment(t *testing.T) {
	n := &NormalizedInvocation{Dir: t.TempDir(), GOFLAGS: "-coverprofile=coverage.txt"}
	p, err := openRetainedCoverage(n)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	fragment := filepath.Join(n.Dir, "fragment")
	if err := os.WriteFile(fragment, []byte("mode: atomic\nwrong"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.Merge(fragment); err == nil {
		t.Fatal("wrong coverage mode accepted")
	}
}

func TestRetainedProfileMergeFailureKeepsBinaryFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("requires /dev/full")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"p.go":      "package p\nfunc Answer() int{return 42}\n",
		"p_test.go": "package p\nimport \"testing\"\nfunc TestOK(t *testing.T){if Answer()!=42{t.Fatal(\"answer\")}}\n",
	})
	retainedSetFlags(n, "-coverprofile=coverage.txt")
	profile, err := openRetainedCoverage(n)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.file.Close(); err != nil {
		t.Fatal(err)
	}
	// Fail the consumer-side merge after the binary has successfully flushed
	// its own profile, independently of the testlog-flush failure case.
	profile.file, err = os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, profile)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if result.ProfileErr == nil || !result.HarnessComplete() || result.BinaryErr != nil || result.ConversionErr != nil {
		t.Fatalf("profile failure replaced binary facts: %+v", result)
	}
	if result.Classify(n, "retained.test").disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatal("failed output merge granted package health")
	}
}
