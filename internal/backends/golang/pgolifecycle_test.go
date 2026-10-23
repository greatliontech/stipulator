package golang

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestPGORetryBuildDriftDeniesGrantAfterRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a retry across a transient compilation-profile change")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs", "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	first, second := pgoTestProfile(t), pgoTestProfile(t)
	dir, profile := pgoLifecycleFixture(t, "profile.pgo", "main", first)
	requirePGOPass(t, runPGOLifecycle(t, dir, false), 1, 0, 0)
	requirePGOPass(t, runPGOLifecycle(t, dir, false), 0, 1, 0)
	oldCheck, oldBuild, oldPublish, oldWave := afterServedCheckForTest, beforePackageBuildForTest, beforePackagePublishForTest, afterRetryWaveForTest
	t.Cleanup(func() {
		afterServedCheckForTest, beforePackageBuildForTest, beforePackagePublishForTest, afterRetryWaveForTest = oldCheck, oldBuild, oldPublish, oldWave
	})
	afterServedCheckForTest = func(pkg string) {
		if pkg == pgoLifecyclePackage {
			writePGOBytes(t, profile, second)
		}
	}
	beforePackageBuildForTest = func(pkg string) {
		if pkg == pgoLifecyclePackage {
			writePGOBytes(t, profile, first)
		}
	}
	beforePackagePublishForTest = func(pkg string) {
		if pkg == pgoLifecyclePackage {
			writePGOBytes(t, profile, second)
		}
	}
	rawPass, typedDrift := false, false
	afterRetryWaveForTest = func(merge *execMerge) {
		for _, row := range merge.rows {
			if row.GetPackage() != pgoLifecyclePackage || row.GetTest() != "TestF" {
				continue
			}
			key := keyOfProducer(row.GetProducer())
			rawPass = row.GetOutcome() == passed && merge.disp[key] == healthy
			var drift *buildInputDrift
			typedDrift = errors.As(merge.obs[key].closeErr, &drift)
		}
	}
	run := runPGOLifecycle(t, dir, false)
	key := pgoLifecyclePackage + ".TestF"
	if !rawPass || !typedDrift || run.Outcomes[key] != verify.TestNotRun || run.NoOutcome[key] == "" || run.Ran != 1 || run.Fresh != 0 || run.Uncached != 1 {
		t.Fatalf("restored profile laundered a retry grant: %+v raw=%v drift=%v", run, rawPass, typedDrift)
	}
	afterServedCheckForTest, beforePackageBuildForTest, beforePackagePublishForTest, afterRetryWaveForTest = oldCheck, oldBuild, oldPublish, oldWave
	requirePGOPass(t, runPGOLifecycle(t, dir, false), 1, 0, 0)
	requirePGOPass(t, runPGOLifecycle(t, dir, false), 0, 1, 0)
}

const pgoLifecyclePackage = "example.com/pgolifecycle/subject"

func TestPGOValidationObservesMetadataWithoutRediscoveringObligations(t *testing.T) {
	if testing.Short() {
		t.Skip("captures policy discovery and re-observes package metadata")
	}
	stipulate.Covers(t, "REQ-check-derivation", "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	dir, _ := pgoLifecycleFixture(t, "", "library", nil)
	pc, err := LoadCapture(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := pc.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.groups) != 1 {
		t.Fatalf("groups = %d", len(discovery.groups))
	}
	counter := countSpawns(t)
	previous := engineCommandHook
	t.Cleanup(func() { engineCommandHook = previous })
	queries := 0
	engineCommandHook = func(cmd *exec.Cmd) {
		queries++
		if len(cmd.Args) == 0 || cmd.Args[len(cmd.Args)-1] != pgoLifecyclePackage || strings.Contains(strings.Join(cmd.Args, " "), "TestGoFiles") {
			t.Errorf("validation widened beyond accepted package metadata: %v", cmd.Args)
		}
	}
	observation, err := observePGO(t.Context(), dir, discovery.groups[0])
	if err != nil {
		t.Fatal(err)
	}
	writePGOPackage(t, dir, "main")
	if err := observation.validate(t.Context()); err == nil || !strings.Contains(err.Error(), "pgo selected package metadata changed") {
		t.Fatalf("metadata motion was not independently observed: %v", err)
	}
	if queries != 2 {
		t.Fatalf("metadata observations = %d, want opening and closing", queries)
	}
	if got := counter.snapshot().derivation(); got != (spawns{}) {
		t.Fatalf("validation repeated accepted-policy derivation: %+v", got)
	}
}

func TestPGONamedAndOffDoNotConsultDefaultSelection(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	dir := t.TempDir()
	writePGOBytes(t, filepath.Join(dir, "profile.pgo"), []byte("profile"))
	for _, mode := range []string{"off", "profile.pgo"} {
		// Source coordinates are the source engine's concern in these modes.
		// No Go command or readable package directory is needed to judge PGO.
		g := &captureGroup{pgo: mode, selectedPackages: []listedPackage{{ImportPath: "unavailable", Name: "main", Dir: filepath.Join(dir, "missing")}}}
		observation, err := observePGO(t.Context(), dir, g)
		if err != nil {
			t.Fatalf("mode %s consulted default selection: %v", mode, err)
		}
		if err := observation.validate(t.Context()); err != nil {
			t.Fatalf("mode %s revalidation consulted default selection: %v", mode, err)
		}
	}
}

func pgoLifecycleFixture(t *testing.T, pgo, name string, profile []byte) (string, string) {
	t.Helper()
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/pgolifecycle\n\ngo 1.27\n",
	})
	writePGOPackage(t, dir, name)
	profilePath := filepath.Join(dir, "subject", "default.pgo")
	if pgo != "" && pgo != "auto" {
		profilePath = filepath.Join(dir, pgo)
	}
	writePGOBytes(t, profilePath, profile)
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./subject"})
	cfg.SetPlainWitness(true)
	cfg.SetAssumePure(true)
	if pgo != "" {
		cfg.SetPgo(pgo)
	}
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", cfg)})
	writePolicyRecord(t, dir, policy)
	return dir, profilePath
}

func writePGOPackage(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "subject"), 0o700); err != nil {
		t.Fatal(err)
	}
	writePGOBytes(t, filepath.Join(dir, "subject", "p.go"), []byte("package "+name+"\nfunc main() {}\nfunc F() int { return 1 }\n"))
	writePGOBytes(t, filepath.Join(dir, "subject", "p_test.go"), []byte("package "+name+"\nimport \"testing\"\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal(\"wrong result\") } }\n"))
}

func writePGOBytes(t *testing.T, path string, bytes []byte) {
	t.Helper()
	if err := os.WriteFile(path, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runPGOLifecycle(t *testing.T, dir string, full bool) *verify.TestRun {
	t.Helper()
	pc, err := LoadCapture(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var run *verify.TestRun
	if full {
		var report *stipulatorv1.ExecutionReport
		report, run, err = ExecutePolicyWitnessed(t.Context(), pc, noSeeding{})
		if err == nil && !SuiteHealthy(report) {
			t.Fatalf("unexpected unhealthy policy: %v", report)
		}
	} else {
		run, err = RunWitnessesPolicy(t.Context(), pc, noSeeding{})
	}
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func requirePGOPass(t *testing.T, run *verify.TestRun, ran, fresh, uncached int) {
	t.Helper()
	if run.Outcomes[pgoLifecyclePackage+".TestF"] != verify.TestPassed || run.Ran != ran || run.Fresh != fresh || run.Uncached != uncached {
		t.Fatalf("want passed ran=%d fresh=%d uncached=%d; got %+v", ran, fresh, uncached, run)
	}
}

func TestPGOProducerClosesOpeningInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("executes plain witnesses across profile changes")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	first, second := pgoTestProfile(t), pgoTestProfile(t)
	for _, tc := range []struct {
		name, pgo      string
		full, prebuild bool
	}{
		{name: "selective-named-prebuild", pgo: "profile.pgo", prebuild: true},
		{name: "health-named-prebuild", pgo: "profile.pgo", full: true, prebuild: true},
		{name: "selective-named-publication", pgo: "profile.pgo"},
		{name: "health-named-publication", pgo: "profile.pgo", full: true},
		{name: "selective-omitted-publication"},
		{name: "health-auto-publication", pgo: "auto", full: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, profile := pgoLifecycleFixture(t, tc.pgo, "main", first)
			savedBuild, savedPublish := beforePackageBuildForTest, beforePackagePublishForTest
			t.Cleanup(func() { beforePackageBuildForTest, beforePackagePublishForTest = savedBuild, savedPublish })
			if tc.prebuild {
				beforePackageBuildForTest = func(pkg string) {
					if pkg == pgoLifecyclePackage {
						writePGOBytes(t, profile, second)
					}
				}
			}
			beforePackagePublishForTest = func(pkg string) {
				if pkg != pgoLifecyclePackage {
					return
				}
				if tc.prebuild {
					// Restoring A cannot clear the prebuild refusal: the binary
					// compiled with B must never publish under the opening A.
					writePGOBytes(t, profile, first)
				} else {
					writePGOBytes(t, profile, second)
				}
			}
			run := runPGOLifecycle(t, dir, tc.full)
			requirePGOPass(t, run, 1, 0, 1)
			cause := run.Degraded + run.UncacheableReasons[pgoLifecyclePackage+".TestF"]
			if !strings.Contains(cause, "pgo build inputs changed") {
				t.Fatalf("profile refusal lost its cause: %+v", run)
			}
			if got := witnesscache.Load(t.Context(), dir).Records; len(got) != 0 {
				t.Fatalf("drifted producer published %d records", len(got))
			}
			beforePackageBuildForTest, beforePackagePublishForTest = savedBuild, savedPublish
			// A stable later operation must recover; refusal is not sticky
			// across engine generations or operations.
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 1, 0, 0)
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 0, 1, 0)
		})
	}
}

func TestPGOServedClosingAndRetryUseCurrentGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("executes plain witnesses and a drift retry")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs", "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	first, second := pgoTestProfile(t), pgoTestProfile(t)
	for _, invalid := range []bool{false, true} {
		name := "new-valid-profile"
		if invalid {
			name = "invalid-profile"
		}
		t.Run(name, func(t *testing.T) {
			dir, profile := pgoLifecycleFixture(t, "", "main", first)
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 1, 0, 0)
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 0, 1, 0)
			saved := afterServedCheckForTest
			t.Cleanup(func() { afterServedCheckForTest = saved })
			fired := false
			afterServedCheckForTest = func(pkg string) {
				if pkg != pgoLifecyclePackage || fired {
					return
				}
				fired = true
				content := second
				if invalid {
					content = []byte("invalid profile")
				}
				writePGOBytes(t, profile, content)
			}
			run := runPGOLifecycle(t, dir, false)
			if !fired || run.Fresh != 0 || !strings.Contains(run.ExecutedReasons[pgoLifecyclePackage+".TestF"], "pgo") {
				t.Fatalf("served profile drift was not rejected: %+v", run)
			}
			if invalid {
				if run.Outcomes[pgoLifecyclePackage+".TestF"] == verify.TestPassed {
					t.Fatalf("invalid current profile retained a passing serve: %+v", run)
				}
			} else {
				requirePGOPass(t, run, 1, 0, 0)
			}
			afterServedCheckForTest = saved
			if invalid {
				writePGOBytes(t, profile, second)
				requirePGOPass(t, runPGOLifecycle(t, dir, false), 1, 0, 0)
			}
			// Publication by the retry must carry B, not A from its old
			// engine. This warm serve is the observable generation check.
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 0, 1, 0)
		})
	}
}

func TestPGODiscoveryMetadataMustStillDescribeTheOpeningBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("executes plain witnesses after a package-name transition")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	profile := pgoTestProfile(t)
	for _, full := range []bool{false, true} {
		name := "selective"
		if full {
			name = "health"
		}
		t.Run(name, func(t *testing.T) {
			dir, _ := pgoLifecycleFixture(t, "", "library", profile)
			saved := beforeGroupEngineForTest
			t.Cleanup(func() { beforeGroupEngineForTest = saved })
			fired := false
			beforeGroupEngineForTest = func() {
				if !fired {
					fired = true
					writePGOPackage(t, dir, "main")
				}
			}
			run := runPGOLifecycle(t, dir, full)
			requirePGOPass(t, run, 1, 0, 1)
			if !fired || !strings.Contains(run.Degraded, "pgo selected package metadata changed") {
				t.Fatalf("stale discovery metadata did not refuse: %+v", run)
			}
			if got := witnesscache.Load(t.Context(), dir).Records; len(got) != 0 {
				t.Fatalf("published using stale discovery names: %d records", len(got))
			}
			beforeGroupEngineForTest = saved
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 1, 0, 0)
			requirePGOPass(t, runPGOLifecycle(t, dir, false), 0, 1, 0)
		})
	}
}
