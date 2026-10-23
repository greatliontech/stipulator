package golang

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedBuildFailureClassification(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"builderr/builderr.go":      "package builderr\nfunc broken() { undefinedIdentifier() }\n",
		"builderr/builderr_test.go": "package builderr\nimport \"testing\"\nfunc TestNeverBuilds(t *testing.T) {}\n",
		"syntax/p_test.go":          "package syntax\nfunc broken( {\n",
		"vet/p_test.go":             "package vet\nimport (\"fmt\"; \"testing\")\nfunc TestBad(t *testing.T) { fmt.Printf(\"%d\", \"wrong\") }\n",
		"dep/p.go":                  "package dep\nvar X = missingDependencyIdentifier\n",
		"consumer/p_test.go":        "package consumer\nimport (_ \"retained.test/dep\"; \"testing\")\nfunc TestDep(t *testing.T) {}\n",
	})
	for _, tc := range []struct{ pkg, diagnostic string }{
		{"builderr", "undefinedIdentifier"},
		{"syntax", "expected"},
		{"vet", "fmt.Printf"},
		{"consumer", "missingDependencyIdentifier"},
		{"dep", "missingDependencyIdentifier"},
	} {
		t.Run(tc.pkg, func(t *testing.T) {
			pkg := "retained.test/" + tc.pkg
			built := buildRetained(t.Context(), n, pkg, retainedProcessHooks{})
			if built.Artifact != nil {
				t.Cleanup(func() { _ = built.Artifact.Close() })
			}
			if built.Stream == nil || built.Err == nil || built.State == nil || built.State.ExitCode() <= 0 {
				t.Fatalf("expected real failed compilation: %+v", built)
			}
			run := runNativePackage(t.Context(), t.Context(), n, pkg, nil, 1, nil, nil, &packageExecution{built: &built})
			if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED {
				t.Fatalf("compiler failure disposition = %v, want BUILD_FAILED; diagnostics=%v", run.disposition, run.diags)
			}
			if len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), tc.diagnostic) {
				t.Fatalf("compiler diagnostic lost: %v", run.diags)
			}
			if run.producer != nil || run.obs != nil || len(run.tests) != 0 {
				t.Fatalf("compilation fabricated execution evidence: %+v", run)
			}
			poisoned := *built.Stream
			poisoned.malformed = "retained compilation ownership: ownership fault"
			refused := classifyRun(n.Name, pkg, &poisoned, built.Err, &built.Stderr, "")
			if refused.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED || !strings.Contains(refused.diags[0].GetOutput(), "ownership fault") {
				t.Fatalf("ownership refusal lost behind compiler failure: %+v", refused)
			}
			// Cancellation and the invocation envelope win even if compiler
			// failure facts became available just before the context ended.
			for _, cause := range []error{context.Canceled, errEnvelopeExpired} {
				ctx, cancel := context.WithCancelCause(t.Context())
				cancel(cause)
				interrupted := runNativePackage(t.Context(), ctx, n, pkg, nil, 1, nil, nil, &packageExecution{built: &built})
				if interrupted.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED {
					t.Fatalf("interruption %v became terminal: %+v", cause, interrupted)
				}
				if cause == errEnvelopeExpired {
					if err := finalizeRun(n, &interrupted, true, ""); err != nil || interrupted.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT {
						t.Fatalf("envelope precedence lost: %+v, %v", interrupted, err)
					}
				}
			}
		})
	}
}

func TestRetainedScopeBuildFailureClassification(t *testing.T) {
	if testing.Short() {
		t.Skip("builds Go fixture package scopes")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"bad/p.go":       "package bad\nfunc broken() { undefinedIdentifier() }\n",
		"bad/p_test.go":  "package bad\nimport \"testing\"\nfunc TestNeverBuilds(t *testing.T) {}\n",
		"good/p_test.go": "package good\nimport \"testing\"\nfunc TestGood(t *testing.T) {}\n",
	})
	retainedSetFlags(n, "-coverpkg=./...")
	scope := buildRetainedScope(t.Context(), n, []string{"retained.test/bad", "retained.test/good"}, retainedProcessHooks{})
	t.Cleanup(func() { _ = scope.Close() })
	built, ok := scope.Packages["retained.test/bad"]
	if !ok || built.Err == nil {
		t.Fatalf("missing scoped build failure: %+v", scope)
	}
	run := runNativePackage(t.Context(), t.Context(), n, "retained.test/bad", nil, 1, nil, nil, &packageExecution{built: &built})
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED || len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), "undefinedIdentifier") {
		t.Fatalf("scoped compiler failure lost: %+v; diagnostics=%v", run, run.diags)
	}
}

func TestRetainedBuildInfrastructureClassification(t *testing.T) {
	if testing.Short() {
		t.Skip("prepares Go fixture packages before testing build failures")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell compiler controls")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\n"})
	const buildFailure = "printf '%s\\n' '{\"Action\":\"build-output\",\"Output\":\"compiler marker\\n\"}' '{\"Action\":\"build-fail\"}'\n"
	for _, tc := range []struct{ name, script, diagnostic string }{
		{"missing artifact", "#!/bin/sh\nexit 0\n", "no test artifact"},
		{"failed start", "#!/nonexistent-stipulator-interpreter\n", "retained compilation"},
		{"silent failure", "#!/bin/sh\necho infrastructure-marker >&2\nexit 1\n", "infrastructure-marker"},
		{"malformed build stream", "#!/bin/sh\n" + buildFailure + "echo invalid-json\nexit 1\n", "malformed stream"},
		{"build failure with successful exit", "#!/bin/sh\n" + buildFailure + "exit 0\n", "compiler marker"},
		{"signaled compiler", "#!/bin/sh\n" + buildFailure + "kill -TERM $$\n", "compiler marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(tc.script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			built := buildRetained(t.Context(), n, "retained.test", retainedProcessHooks{})
			if built.Artifact != nil {
				t.Cleanup(func() { _ = built.Artifact.Close() })
			}
			if built.Err == nil {
				t.Fatalf("faulty compiler accepted: %+v", built)
			}
			run := runNativePackage(t.Context(), t.Context(), n, "retained.test", nil, 1, nil, nil, &packageExecution{built: &built})
			if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
				t.Fatalf("infrastructure disposition = %v, want DEGRADED; diagnostics=%v", run.disposition, run.diags)
			}
			if len(run.diags) != 1 || !strings.Contains(run.diags[0].GetOutput(), tc.diagnostic) {
				t.Fatalf("infrastructure diagnostic lost: %v", run.diags)
			}
		})
	}
}
