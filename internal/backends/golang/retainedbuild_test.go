package golang

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

// retainedFixtureInvocation starts real toolchain work over a fixture tree,
// which belongs to the full tier rather than the fast tool-code tier.
func retainedFixtureInvocation(t *testing.T, files map[string]string) *NormalizedInvocation {
	t.Helper()
	files["go.mod"] = "module retained.test\n\ngo 1.25\n"
	dir := writeModule(t, files)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("retained")
	inv.SetTimeout(durationpb.New(time.Minute))
	inv.SetGo(cfg)
	n, err := NormalizeInvocation(t.Context(), dir, inv)
	if err != nil {
		t.Fatal(err)
	}
	obligations, err := DiscoverInvocation(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	freezeWitnessAllocation(n, len(selectedPackages(obligations)))
	return n
}

func retainedSetFlags(n *NormalizedInvocation, flags string) {
	n.GOFLAGS = flags
	n.Env = gotool.SetEnv(n.Env, "GOFLAGS", flags)
	n.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "GOFLAGS", flags)
}

func retainedBuildOK(t *testing.T, n *NormalizedInvocation, pkg string) *retainedArtifact {
	t.Helper()
	r := buildRetained(t.Context(), n, pkg, retainedProcessHooks{})
	if r.Err != nil || r.Artifact == nil {
		t.Fatalf("compile %s: %v; stderr=%s; stream=%+v", pkg, r.Err, r.Stderr.b.String(), r.Stream)
	}
	t.Cleanup(func() {
		if err := r.Artifact.Close(); err != nil {
			t.Error(err)
		}
	})
	return r.Artifact
}

func retainedRuntimeFor(t *testing.T, n *NormalizedInvocation, a *retainedArtifact, pkg string, selection []string, profile *retainedCoverageProfile, coverageArtifacts ...*retainedArtifact) *retainedRuntime {
	t.Helper()
	dir := n.Dir
	if pkg != "retained.test" {
		dir = filepath.Join(dir, strings.TrimPrefix(pkg, "retained.test/"))
	}
	snapshot, err := rootsRunner.TakeEnvSnapshot(t.Context(), n.Dir, witnessEnvOf(n))
	if err != nil {
		t.Fatal(err)
	}
	if len(coverageArtifacts) == 0 {
		coverageArtifacts = []*retainedArtifact{a}
	}
	environment, err := prepareRetainedEnvironment(n, dir, snapshot.Value("GOROOT"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Close(); err != nil {
			t.Error(err)
		}
	})
	r, err := prepareRetainedRuntime(t.Context(), n, a, retainedRuntimeOptions{Package: pkg, Environment: environment, GOCACHE: snapshot.Value("GOCACHE"), Selection: selection, Coverage: profile, CoverageArtifacts: coverageArtifacts}, retainedProcessHooks{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func TestRetainedBuildVetsInternalExternalAndDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"internal/bad_test.go": "package internal\nimport (\"fmt\"; \"testing\")\nfunc TestBad(t *testing.T) { fmt.Printf(\"%d\", \"wrong\") }\n",
		"external/p.go":        "package external\n",
		"external/bad_test.go": "package external_test\nimport (\"fmt\"; \"testing\")\nfunc TestBad(t *testing.T) { fmt.Printf(\"%d\", \"wrong\") }\n",
		"dep/p.go":             "package dep\nvar X = missing\n",
		"consumer/p_test.go":   "package consumer\nimport (_ \"retained.test/dep\"; \"testing\")\nfunc TestDep(t *testing.T) {}\n",
	})
	for _, pkg := range []string{"internal", "external", "consumer"} {
		t.Run(pkg, func(t *testing.T) {
			r := buildRetained(t.Context(), n, "retained.test/"+pkg, retainedProcessHooks{})
			if r.Err == nil || r.Artifact != nil || r.State == nil || r.State.Success() {
				t.Fatalf("bad package compiled: %+v", r)
			}
			if r.Stream == nil || !r.Stream.failedBuild {
				t.Fatalf("build diagnostics missing: %+v, stderr %s", r.Stream, r.Stderr.b.String())
			}
		})
	}
}

func TestRetainedBuildTestlessAndHelperOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"testless/p.go":    "package testless\nfunc Answer() int { return 42 }\n",
		"helper/p_test.go": "package helper\nfunc helper() {}\n",
	})
	r := buildRetained(t.Context(), n, "retained.test/testless", retainedProcessHooks{})
	if r.Err != nil || !r.NoTestFiles || r.Artifact != nil {
		t.Fatalf("testless build = %+v; stream=%+v", r, r.Stream)
	}
	a := retainedBuildOK(t, n, "retained.test/helper")
	run := runRetained(t.Context(), t.Context(), n, retainedRuntimeFor(t, n, a, "retained.test/helper", nil, nil), &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !run.HarnessComplete() || run.BinaryState == nil || !run.BinaryState.Success() {
		t.Fatalf("helper-only package did not execute: %+v; stream=%+v", run, run.Stream)
	}
}

func TestRetainedBuildRejectsMissingArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell compiler fixture")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\n"})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := buildRetained(context.Background(), n, "retained.test", retainedProcessHooks{})
	if r.Err == nil || r.Artifact != nil || !strings.Contains(r.Err.Error(), "no test artifact") {
		t.Fatalf("fake compiler accepted: %+v", r)
	}
}
