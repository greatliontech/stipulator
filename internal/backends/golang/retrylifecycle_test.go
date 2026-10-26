package golang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func retryLifecyclePolicy(t *testing.T, dir string) {
	t.Helper()
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetPlainWitness(true)
	cfg.SetPgo("off")
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("plain", cfg)})
	writePolicyRecord(t, dir, policy)
}

func TestRetryWavesRecheckSurvivingServesAndBoundCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("executes chained and cyclic package interference")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation", "REQ-evidence-witness-freshness-interference")
	neutralAmbient(t)
	for _, cycle := range []bool{false, true} {
		name := "chain"
		if cycle {
			name = "cycle"
		}
		t.Run(name, func(t *testing.T) {
			action := `if string(data) != "old" { t.Fatalf("changed input: %s", data) }`
			if cycle {
				action = `if string(data) == "moved" { if err := os.WriteFile("../b/trigger", []byte("again"), 0600); err != nil { t.Fatal(err) } }`
			}
			writer := "package c\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestC(t *testing.T) { if armed { if err := os.WriteFile(\"../b/trigger\", []byte(\"yes\"), 0600); err != nil { t.Fatal(err) } } }\n"
			dir := writeModule(t, map[string]string{
				"go.mod": "module example.com/waves\n\ngo 1.26\n",
				"a/data": "old", "b/trigger": "no",
				"a/a_test.go": "package a\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestA(t *testing.T) { data, err := os.ReadFile(\"data\"); if err != nil { t.Fatal(err) }; " + action + " }\n",
				"b/b_test.go": "package b\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestB(t *testing.T) { data, err := os.ReadFile(\"trigger\"); if err != nil { t.Fatal(err) }; if string(data) == \"yes\" { if err := os.WriteFile(\"../a/data\", []byte(\"moved\"), 0600); err != nil { t.Fatal(err) } } }\n",
				"c/c_test.go": writer,
			})
			retryLifecyclePolicy(t, dir)
			cold, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || cold.Ran != 3 || cold.Uncached != 0 {
				t.Fatalf("cold controls: %+v %v", cold, err)
			}
			warm, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || warm.Fresh != 3 || warm.Ran != 0 {
				t.Fatalf("warm controls: %+v %v", warm, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "c/c_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
				t.Fatal(err)
			}
			attempts := map[string]int{}
			ordinals := map[int32]bool{}
			rawBPassed := false
			prior := afterRetryWaveForTest
			afterRetryWaveForTest = func(merge *execMerge) {
				for _, row := range merge.rows {
					if row.GetTest() != topLevel(row.GetTest()) {
						continue
					}
					attempts[row.GetPackage()]++
					ordinal := row.GetProducer().GetProcessOrdinal()
					if ordinals[ordinal] {
						t.Errorf("separate retry waves reused process ordinal %d", ordinal)
					}
					ordinals[ordinal] = true
					if row.GetPackage() == "example.com/waves/b" && row.GetOutcome() == passed && merge.disp[keyOfProducer(row.GetProducer())] == healthy {
						rawBPassed = true
					}
				}
			}
			t.Cleanup(func() { afterRetryWaveForTest = prior })
			run, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || run.Fresh != 0 || run.Ran != 3 {
				t.Fatalf("retry waves: %+v %v", run, err)
			}
			if attempts["example.com/waves/a"] != 1 || attempts["example.com/waves/b"] != 1 || len(attempts) != 2 {
				t.Fatalf("logical retries were missing or repeated: %v", attempts)
			}
			if run.Outcomes["example.com/waves/c.TestC"] != verify.TestPassed {
				t.Fatal("ordinary initial execution lost its outcome")
			}
			if !cycle {
				if run.Outcomes["example.com/waves/a.TestA"] != verify.TestFailed || run.Outcomes["example.com/waves/b.TestB"] != verify.TestPassed {
					t.Fatalf("the C -> B -> A chain kept A's cached pass: %+v", run)
				}
			} else {
				if !rawBPassed || run.Outcomes["example.com/waves/b.TestB"] != verify.TestNotRun || run.NoOutcome["example.com/waves/b.TestB"] == "" || run.Outcomes["example.com/waves/a.TestA"] != verify.TestPassed || run.Uncached != 1 {
					t.Fatalf("cyclic interference retained a stale retry grant or erased execution facts: %+v rawBPassed=%v", run, rawBPassed)
				}
			}
			if rec := cacheRecord(t, witnesscache.Load(t.Context(), dir).Records, "example.com/waves/c", "TestC"); rec == nil {
				t.Fatal("later retries erased the independently completed initial deposit")
			}
		})
	}
}

func TestRetryDriftDeniesOnlyTheGrantNotTheExecutedPass(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a passing retry with source motion and an uncacheable control")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation", "REQ-evidence-outcome-premises")
	neutralAmbient(t)
	for _, moves := range []bool{true, false} {
		name := "source-moves-before-close"
		if !moves {
			name = "unsupported-but-stable"
		}
		t.Run(name, func(t *testing.T) {
			initial := "package reader\nimport (\"os\"; \"testing\")\nconst revision = 1\nfunc TestReads(t *testing.T) { if os.Getenv(\"PWD\") == \"\" || revision < 1 { t.Fatal(\"setup\") } }\n"
			replacement := strings.Replace(initial, "revision = 1", "revision = 2", 1)
			if !moves {
				replacement = "package reader\nimport (\"os\"; \"testing\")\nfunc TestReads(t *testing.T) { if _, err := os.ReadFile(\"data\"); err != nil { t.Fatal(err) } }\n"
			}
			writer := fmt.Sprintf("package writer\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestWrites(t *testing.T) { if armed { if err := os.WriteFile(\"../reader/r_test.go\", []byte(%q), 0600); err != nil { t.Fatal(err) } } }\n", replacement)
			dir := writeModule(t, map[string]string{"go.mod": "module example.com/retrygrant\n\ngo 1.26\n", "reader/r_test.go": initial, "reader/data": "fixture", "writer/w_test.go": writer})
			retryLifecyclePolicy(t, dir)
			cold, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || cold.Uncached != 0 {
				t.Fatalf("cold control: %+v %v", cold, err)
			}
			original := cacheRecord(t, witnesscache.Load(t.Context(), dir).Records, "example.com/retrygrant/reader", "TestReads")
			if original == nil {
				t.Fatal("no cached reader to invalidate")
			}
			if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
				t.Fatal(err)
			}
			priorPublish, priorWave := beforePackagePublishForTest, afterRetryWaveForTest
			t.Cleanup(func() { beforePackagePublishForTest, afterRetryWaveForTest = priorPublish, priorWave })
			beforePackagePublishForTest = func(pkg string) {
				if moves && pkg == "example.com/retrygrant/reader" {
					if err := os.WriteFile(filepath.Join(dir, "reader/r_test.go"), []byte(strings.Replace(replacement, "revision = 2", "revision = 3", 1)), 0600); err != nil {
						t.Error(err)
					}
				}
			}
			rawPass, rawHealthy, typedDrift := false, false, false
			afterRetryWaveForTest = func(merge *execMerge) {
				for _, row := range merge.rows {
					if row.GetPackage() != "example.com/retrygrant/reader" || row.GetTest() != "TestReads" {
						continue
					}
					key := keyOfProducer(row.GetProducer())
					rawPass = row.GetOutcome() == passed
					rawHealthy = merge.disp[key] == healthy
					typedDrift = errors.Is(merge.obs[key].closeErr, gofresh.ErrViewChanged)
				}
			}
			run, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || !rawPass || !rawHealthy || run.Ran != 2 || run.Fresh != 0 || run.Uncached != 1 {
				t.Fatalf("retry facts: %+v %v rawPass=%v healthy=%v", run, err, rawPass, rawHealthy)
			}
			key := "example.com/retrygrant/reader.TestReads"
			if moves {
				if !typedDrift || run.Outcomes[key] != verify.TestNotRun || run.NoOutcome[key] == "" {
					t.Fatalf("known retry drift still granted a witness: %+v typed=%v", run, typedDrift)
				}
			} else if typedDrift || run.Outcomes[key] != verify.TestPassed {
				t.Fatalf("mere uncacheability lost the actual passing witness: %+v typed=%v", run, typedDrift)
			}
			for _, record := range witnesscache.Load(t.Context(), dir).Records {
				if record.Package == original.Package && record.Test == original.Test && record.Fingerprint != original.Fingerprint {
					t.Fatal("refused retry published a new record")
				}
			}
		})
	}
}

// A starts served beside B. C's initial execution invalidates B; only B's
// retry then invalidates A's two subjects. Scope must be applied to this later
// wave too, revoking the out-of-scope serve without executing it. A's in-scope
// co-member is a positive control for both the new wave and its frozen width.
func TestRetryWavesScopeLateDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a scoped chain through two independently checked retry waves")
	}
	stipulate.Covers(t, "REQ-check-verdict-scoped", "REQ-evidence-witness-freshness-concurrency", "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	budget := runtime.GOMAXPROCS(0)
	t.Setenv("GOMAXPROCS", strconv.Itoa(budget))
	slots := 3
	if budget < 6 {
		slots = max(1, budget/2)
	}
	width := budget / slots
	reader := fmt.Sprintf(`package a
import ("os"; "runtime"; "testing")
func TestIn(t *testing.T) {
 if _,err := os.ReadFile("data"); err != nil { t.Fatal(err) }
 if got := os.Getenv("GOMAXPROCS"); got != %q { t.Fatalf("environment width = %%s", got) }
 if got := runtime.GOMAXPROCS(0); got != %d { t.Fatalf("runtime width = %%d", got) }
}
func TestOut(t *testing.T) {
 data,err := os.ReadFile("data"); if err != nil { t.Fatal(err) }
 if string(data) != "old" { t.Fatal("late out-of-scope retry executed") }
}
`, strconv.Itoa(width), width)
	bridge := `package b
import ("os"; "testing")
func TestB(t *testing.T) {
 data,err := os.ReadFile("trigger"); if err != nil { t.Fatal(err) }
 if string(data) == "yes" { if err := os.WriteFile("../a/data", []byte("moved"),0600); err != nil { t.Fatal(err) } }
}
`
	writer := `package c
import ("os"; "testing")
const armed = false
func TestC(t *testing.T) {
 if armed { if err := os.WriteFile("../b/trigger",[]byte("yes"),0600); err != nil { t.Fatal(err) } }
}
`
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/scopedwaves\n\ngo 1.26\n",
		"a/data": "old", "b/trigger": "no",
		"a/a_test.go": reader, "b/b_test.go": bridge, "c/c_test.go": writer,
	})
	retryLifecyclePolicy(t, dir)
	if cold, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || cold.Ran != 4 || cold.Uncached != 0 {
		t.Fatalf("cold controls: %+v %v", cold, err)
	}
	if warm, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || warm.Fresh != 4 || warm.Ran != 0 {
		t.Fatalf("warm controls: %+v %v", warm, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c/c_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	scope := map[gofresh.Subject]bool{
		{Package: "example.com/scopedwaves/a", Symbol: "TestIn"}: true,
		{Package: "example.com/scopedwaves/b", Symbol: "TestB"}:  true,
		{Package: "example.com/scopedwaves/c", Symbol: "TestC"}:  true,
	}
	pc, err := LoadCapture(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := pc.ConfigureExecution(false, scope); err != nil {
		t.Fatal(err)
	}
	d, err := pc.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.groups) != 1 || pc.normalized[0].SpawnBound != slots {
		t.Fatalf("pre-capture population: groups=%d bound=%d", len(d.groups), pc.normalized[0].SpawnBound)
	}
	frozen := append([]string(nil), pc.normalized[0].WitnessEnv...)
	group, identity := d.groups[0], groupIdentity(pc.normalized[0])
	if value, _ := lookupEnv(group.witnessEnv, "GOMAXPROCS"); value != strconv.Itoa(width) {
		t.Fatalf("capture width=%s want %d", value, width)
	}
	var waves [][]string
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) {
		var names []string
		for _, row := range merge.rows {
			if row.GetTest() != topLevel(row.GetTest()) {
				continue
			}
			names = append(names, row.GetPackage()+"."+row.GetTest())
			if row.GetOutcome() != passed || merge.disp[keyOfProducer(row.GetProducer())] != healthy {
				t.Errorf("retry was not an actual healthy pass: %v", row)
			}
		}
		waves = append(waves, names)
		for _, observation := range merge.obs {
			if value, _ := lookupEnv(observation.runtime.Environment, "GOMAXPROCS"); value != strconv.Itoa(width) {
				t.Errorf("actual retry description changed width to %s", value)
			}
		}
		if !reflect.DeepEqual(pc.normalized[0].WitnessEnv, frozen) || !reflect.DeepEqual(group.witnessEnv, frozen) || groupIdentity(pc.normalized[0]) != identity || pc.normalized[0].SpawnBound != slots {
			t.Error("retry changed the pre-capture allocation or identity")
		}
	}
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	run, err := RunWitnessesScoped(t.Context(), pc, scope, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"example.com/scopedwaves/b.TestB"}, {"example.com/scopedwaves/a.TestIn"}}
	if !reflect.DeepEqual(waves, want) {
		t.Fatalf("late-wave scope was not enforced: actual retries=%v", waves)
	}
	if run.Degraded != "" || len(run.Diagnostics) != 0 || run.Fresh != 0 || run.Ran != 3 {
		t.Fatalf("scoped retry accounting: %+v", run)
	}
	for subject := range scope {
		if run.Outcomes[subject.Package+"."+subject.Symbol] != verify.TestPassed {
			t.Fatalf("in-scope control lost its grant: %+v", run)
		}
	}
	out := "example.com/scopedwaves/a.TestOut"
	if _, exists := run.Outcomes[out]; exists || !run.ScopeSkipped[out] || run.NoOutcome[out] != "" || run.ExecutedReasons[out] != "" {
		t.Fatalf("late invalid serve was neither withheld nor correctly scope-skipped: %+v", run)
	}
}
