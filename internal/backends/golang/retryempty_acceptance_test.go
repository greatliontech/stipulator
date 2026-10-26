package golang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestRetryCompleteEmptyPopulationRevokesEarlierGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a retry chain that removes every runnable, then judges the empty suite")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation", "REQ-evidence-witness-freshness-interference", "REQ-go-retained-test-execution")
	neutralAmbient(t)
	// Purity permits the intentionally interfering fixtures to seed reusable
	// records. It must not override their runtime-input or current-source guards.
	sources := map[string]string{
		"a/a_test.go": `package a
import ("os"; "strings"; "testing")
func TestA(t *testing.T) {
 data, err := os.ReadFile("data"); if err != nil { t.Fatal(err) }
 if string(data) == "moved" {
  for _, path := range []string{"../a/a_test.go", "../b/b_test.go", "../c/c_test.go"} {
   source, err := os.ReadFile(path); if err != nil { t.Fatal(err) }
   if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(source), "func Test", "func helper")), 0600); err != nil { t.Fatal(err) }
  }
 }
}
`,
		"b/b_test.go": `package b
import ("os"; "testing")
func TestB(t *testing.T) {
 data, err := os.ReadFile("trigger"); if err != nil { t.Fatal(err) }
 if string(data) == "yes" { if err := os.WriteFile("../a/data", []byte("moved"), 0600); err != nil { t.Fatal(err) } }
}
`,
		"c/c_test.go": `package c
import ("os"; "testing")
const armed = false
func TestC(t *testing.T) {
 if armed { if err := os.WriteFile("../b/trigger", []byte("yes"), 0600); err != nil { t.Fatal(err) } }
}
`,
	}
	files := map[string]string{"go.mod": "module example.com/emptyretry\n\ngo 1.26\n", "a/data": "old", "b/trigger": "no"}
	for path, source := range sources {
		files[path] = source
	}
	dir := writeModule(t, files)
	retryLifecyclePolicy(t, dir) // PGO off keeps this a runnable-population test.
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Ran != 3 || run.Uncached != 0 {
		t.Fatalf("cold: %+v %v", run, err)
	}
	for _, subject := range []struct{ pkg, name string }{{"a", "TestA"}, {"b", "TestB"}, {"c", "TestC"}} {
		record := cacheRecord(t, witnesscache.Load(t.Context(), dir).Records, "example.com/emptyretry/"+subject.pkg, subject.name)
		if record == nil || record.Fingerprint.PurityAssertion == "" {
			t.Fatalf("fixture did not seed the intended pure record: %s.%s", subject.pkg, subject.name)
		}
	}
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Fresh != 3 || run.Ran != 0 {
		t.Fatalf("warm: %+v %v", run, err)
	}
	sources["c/c_test.go"] = strings.Replace(sources["c/c_test.go"], "armed = false", "armed = true", 1)
	if err := os.WriteFile(filepath.Join(dir, "c/c_test.go"), []byte(sources["c/c_test.go"]), 0600); err != nil {
		t.Fatal(err)
	}
	var waves []*execMerge
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) { waves = append(waves, merge) }
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	run, err := RunWitnesses(t.Context(), dir, noSeeding{})
	afterRetryWaveForTest = prior
	if err != nil {
		t.Fatal(err)
	}
	// Check the actual files, not a synthetic empty native snapshot. The next
	// invocation below also compiles these helper-only test packages successfully.
	for path, source := range sources {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil || string(got) != strings.ReplaceAll(source, "func Test", "func helper") {
			t.Fatalf("complete helper-only generation missing at %s: %s %v", path, got, err)
		}
	}
	attempts := map[string]int{}
	ordinals := map[int32]bool{}
	for _, wave := range waves {
		if len(wave.rows) != 1 || len(wave.disp) != 1 || len(wave.obs) != 1 {
			t.Fatalf("retry wave invented or lost process facts: %+v", wave)
		}
		row := wave.rows[0]
		key := keyOfProducer(row.GetProducer())
		subject := row.GetPackage() + "." + row.GetTest()
		attempts[subject]++
		if row.GetOutcome() != passed || wave.disp[key] != healthy || row.GetProducer().GetProcessId() <= 0 || ordinals[row.GetProducer().GetProcessOrdinal()] {
			t.Fatalf("retry execution facts changed: row=%v dispositions=%v", row, wave.disp)
		}
		ordinals[row.GetProducer().GetProcessOrdinal()] = true
		observation := wave.obs[key]
		if observation == nil || observation.account.Terminal.Kind != gofresh.NormalCompletion {
			t.Fatalf("retry lost its actual normal completion: %s observation=%+v", subject, observation)
		}
		switch subject {
		case "example.com/emptyretry/b.TestB":
			if observation.closeErr != nil || len(observation.final.Subjects) != 1 || observation.final.Subjects[0].Fingerprint.PurityAssertion == "" {
				t.Fatalf("B did not independently complete and close before its later removal: %+v", observation)
			}
		case "example.com/emptyretry/a.TestA":
			// A removed its own declaration during a genuinely passing retry.
			// Its failed native close supplies no fingerprint; that absence must
			// not exclude the measured attempt from current-membership judgment.
			if observation.closeErr == nil || len(observation.final.Subjects) != 0 {
				t.Fatalf("A no longer exercises a real closing refusal without finalized subject evidence: %+v", observation)
			}
		}
	}
	if len(waves) != 2 || len(attempts) != 2 || attempts["example.com/emptyretry/b.TestB"] != 1 || attempts["example.com/emptyretry/a.TestA"] != 1 || run.Ran != 3 || run.Fresh != 0 {
		t.Fatalf("empty population restored a spent retry budget or hid work: attempts=%v run=%+v", attempts, run)
	}
	for _, removed := range []string{"example.com/emptyretry/a.TestA", "example.com/emptyretry/b.TestB"} {
		if run.Outcomes[removed] != verify.TestNotRun || run.NoOutcome[removed] == "" {
			t.Errorf("observed empty current population retained %s's passing retry grant: %+v", removed, run)
		}
	}
	if run.Outcomes["example.com/emptyretry/c.TestC"] != verify.TestPassed || run.Uncached != 2 {
		t.Errorf("retry revocation changed the initial execution or lost the uncached account: %+v", run)
	}

	// A new whole invocation now starts with a genuinely empty runnable set.
	// Its three helper-only test packages still build and run empty harnesses;
	// neither those processes nor the old cache can invent subject evidence.
	pc, err := LoadCapture(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	report, empty, err := ExecutePolicyWitnessed(t.Context(), pc, noSeeding{})
	if err != nil || !SuiteHealthy(report) || empty.Ran != 0 || empty.Fresh != 0 || empty.Uncached != 0 || len(empty.Outcomes) != 0 || len(report.GetTests()) != 0 {
		t.Fatalf("initial empty population lost build/health work or invented witnesses: report=%v run=%+v err=%v", report, empty, err)
	}
	packages := map[string]bool{}
	for _, observation := range report.GetObservations() {
		pkg := observation.GetPackage()
		if packages[pkg] || observation.GetProducer().GetProcessId() <= 0 || observation.GetCompleted() != nil || observation.GetIncompleteReason() == "" {
			t.Fatalf("empty invocation invented a native observation or duplicated a process: %v", observation)
		}
		packages[pkg] = true
	}
	if len(packages) != 3 || !packages["example.com/emptyretry/a"] || !packages["example.com/emptyretry/b"] || !packages["example.com/emptyretry/c"] {
		t.Fatalf("helper-only package execution was lost: %v", packages)
	}
}

func TestRetryUnavailableCurrentPopulationKeepsEarlierMeasuredPass(t *testing.T) {
	if testing.Short() {
		t.Skip("makes current source unavailable after a real passing retry")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	reader := "package reader\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestRead(t *testing.T) { if _, err := os.ReadFile(\"data\"); err != nil { t.Fatal(err) } }\n"
	writer := "package writer\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestWriter(t *testing.T) { if armed { if err := os.WriteFile(\"../reader/data\", []byte(\"new\"), 0600); err != nil { t.Fatal(err) } } }\n"
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/unavailableretry\n\ngo 1.26\n", "reader/data": "old", "reader/r_test.go": reader, "writer/w_test.go": writer})
	retryLifecyclePolicy(t, dir)
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Ran != 2 || run.Uncached != 0 {
		t.Fatalf("cold: %+v %v", run, err)
	}
	if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Fresh != 2 || run.Ran != 0 {
		t.Fatalf("warm: %+v %v", run, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte(strings.Replace(writer, "armed = false", "armed = true", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	pc, err := LoadCapture(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	var waves []*execMerge
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) {
		waves = append(waves, merge)
		// The reader has already closed. A different selected package now has
		// unreadable syntax, so the next complete current inventory is unknown,
		// not an observation that this unchanged reader disappeared.
		if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte("package writer\nfunc (\n"), 0600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	run, err := RunWitnessesPolicy(t.Context(), pc, noSeeding{})
	afterRetryWaveForTest = prior
	if err != nil || run.Ran != 2 || run.Fresh != 0 || run.Uncached != 1 || len(waves) != 1 {
		t.Fatalf("unavailable-current-inventory accounting: %+v %v waves=%d", run, err, len(waves))
	}
	wave := waves[0]
	if len(wave.rows) != 1 || len(wave.disp) != 1 || len(wave.obs) != 1 {
		t.Fatalf("unavailable inventory invented retry work: %+v", wave)
	}
	row := wave.rows[0]
	key := keyOfProducer(row.GetProducer())
	observation := wave.obs[key]
	if row.GetPackage() != "example.com/unavailableretry/reader" || row.GetTest() != "TestRead" || row.GetOutcome() != passed || wave.disp[key] != healthy || observation == nil || observation.closeErr != nil || observation.account.Terminal.Kind != gofresh.NormalCompletion {
		t.Fatalf("missing independently measured healthy reader: row=%v observation=%+v", row, observation)
	}
	got, err := os.ReadFile(filepath.Join(dir, "reader/r_test.go"))
	if err != nil || string(got) != reader {
		t.Fatalf("the control changed the reader source: %s %v", got, err)
	}
	discovery, err := pc.discover(t.Context())
	if err != nil || len(discovery.groups) != 1 {
		t.Fatalf("fixture capture groups: %+v %v", discovery, err)
	}
	if snapshot, _, err := groupSnapshot(t.Context(), dir, discovery.groups[0]); err == nil || snapshot != nil {
		t.Fatalf("malformed selected source unexpectedly supplied a complete current generation: %+v %v", snapshot, err)
	}
	subject := "example.com/unavailableretry/reader.TestRead"
	if run.Outcomes[subject] != verify.TestPassed || run.NoOutcome[subject] != "" || run.UncacheableReasons[subject] == "" || run.Outcomes["example.com/unavailableretry/writer.TestWriter"] != verify.TestPassed {
		t.Fatalf("unavailable population became evidence of removal: %+v", run)
	}
}

func TestRetryMissingFingerprintKeepsMeasuredPassWithoutDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("refuses native closing after real retries with present and unavailable current populations")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	for _, name := range []string{"present", "unavailable"} {
		t.Run(name, func(t *testing.T) {
			reader := "package reader\nimport (\"os\"; \"testing\")\n//gofresh:pure\nfunc TestRead(t *testing.T) { if _, err := os.ReadFile(\"data\"); err != nil { t.Fatal(err) } }\n"
			writer := "package writer\nimport (\"os\"; \"testing\")\nconst armed = false\n//gofresh:pure\nfunc TestWriter(t *testing.T) { if armed { if err := os.WriteFile(\"../reader/data\", []byte(\"new\"), 0600); err != nil { t.Fatal(err) } } }\n"
			dir := writeModule(t, map[string]string{"go.mod": "module example.com/nofpretry\n\ngo 1.26\n", "reader/data": "old", "reader/r_test.go": reader, "writer/w_test.go": writer})
			retryLifecyclePolicy(t, dir)
			if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Ran != 2 || run.Uncached != 0 {
				t.Fatalf("cold: %+v %v", run, err)
			}
			original := cacheRecord(t, witnesscache.Load(t.Context(), dir).Records, "example.com/nofpretry/reader", "TestRead")
			if original == nil {
				t.Fatal("reader has no cached pass to invalidate")
			}
			if run, err := RunWitnesses(t.Context(), dir, noSeeding{}); err != nil || run.Fresh != 2 || run.Ran != 0 {
				t.Fatalf("warm: %+v %v", run, err)
			}
			writer = strings.Replace(writer, "armed = false", "armed = true", 1)
			if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte(writer), 0600); err != nil {
				t.Fatal(err)
			}
			pc, err := LoadCapture(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			priorClose, priorPublish, priorWave := closeNativeProcess, beforePackagePublishForTest, afterRetryWaveForTest
			t.Cleanup(func() {
				closeNativeProcess, beforePackagePublishForTest, afterRetryWaveForTest = priorClose, priorPublish, priorWave
			})
			fault := errors.New("fixture native closing infrastructure unavailable")
			faultNextClose, refused := false, 0
			beforePackagePublishForTest = func(pkg string) { faultNextClose = pkg == "example.com/nofpretry/reader" }
			closeNativeProcess = func(ctx context.Context, unit *gofresh.PackageUnit) (gofresh.FinalizedProcess, error) {
				if faultNextClose {
					faultNextClose = false
					refused++
					// The real retained binary has already passed and submitted its
					// completion. Refuse closing through the infrastructure seam;
					// no finalized observation or fingerprint is edited after the fact.
					return gofresh.FinalizedProcess{}, fault
				}
				return priorClose(ctx, unit)
			}
			var waves []*execMerge
			afterRetryWaveForTest = func(merge *execMerge) {
				waves = append(waves, merge)
				if name == "unavailable" {
					if err := os.WriteFile(filepath.Join(dir, "writer/w_test.go"), []byte("package writer\nfunc (\n"), 0600); err != nil {
						t.Error(err)
					}
				}
			}
			run, err := RunWitnessesPolicy(t.Context(), pc, noSeeding{})
			closeNativeProcess, beforePackagePublishForTest, afterRetryWaveForTest = priorClose, priorPublish, priorWave
			if err != nil || refused != 1 || len(waves) != 1 || run.Ran != 2 || run.Fresh != 0 || run.Uncached != 1 {
				t.Fatalf("retry close-failure accounting: %+v %v refused=%d waves=%d", run, err, refused, len(waves))
			}
			wave := waves[0]
			if len(wave.rows) != 1 || len(wave.disp) != 1 || len(wave.obs) != 1 {
				t.Fatalf("missing-fingerprint retry invented process facts: %+v", wave)
			}
			row := wave.rows[0]
			key := keyOfProducer(row.GetProducer())
			observation := wave.obs[key]
			if row.GetPackage() != "example.com/nofpretry/reader" || row.GetTest() != "TestRead" || row.GetOutcome() != passed || row.GetProducer().GetProcessId() <= 0 || wave.disp[key] != healthy || observation == nil || !errors.Is(observation.closeErr, fault) || observation.account.Terminal.Kind != gofresh.NormalCompletion || len(observation.final.Subjects) != 0 {
				t.Fatalf("fixture lacks a genuinely passed retry without finalized subject evidence: row=%v observation=%+v", row, observation)
			}
			got, err := os.ReadFile(filepath.Join(dir, "reader/r_test.go"))
			if err != nil || string(got) != reader {
				t.Fatalf("the control changed the reader declaration: %s %v", got, err)
			}
			query := *pc.normalized[0]
			query.Packages = []string{"example.com/nofpretry/reader", "example.com/nofpretry/writer"}
			population, err := observeRunnableGeneration(t.Context(), query)
			if name == "unavailable" {
				if err == nil || population != nil {
					t.Fatalf("malformed sibling supplied a complete current population: %+v %v", population, err)
				}
			} else if err != nil || population == nil || !slices.Contains(population.subjects(), gofresh.Subject{Package: "example.com/nofpretry/reader", Symbol: "TestRead"}) {
				t.Fatalf("unchanged reader missing from complete current population: %+v %v", population, err)
			}
			subject := "example.com/nofpretry/reader.TestRead"
			if run.Outcomes[subject] != verify.TestPassed || run.NoOutcome[subject] != "" || run.UncacheableReasons[subject] == "" || run.Outcomes["example.com/nofpretry/writer.TestWriter"] != verify.TestPassed {
				t.Fatalf("missing optional fingerprint became evidence of retry drift: %+v", run)
			}
			for _, record := range witnesscache.Load(t.Context(), dir).Records {
				if record.Package == original.Package && record.Test == original.Test && record.Fingerprint != original.Fingerprint {
					t.Fatal("failed native close invented a new reusable reader fingerprint")
				}
			}
		})
	}
}
