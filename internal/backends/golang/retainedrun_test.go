package golang

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestRetainedEOFFragmentsCannotCompleteHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes a Go fixture")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("fmt"; "os"; "testing")
func TestMain(m *testing.M) {
 if os.Getenv("RUN") == "yes" { m.Run() }
 fmt.Print(os.Getenv("TAIL"))
 os.Exit(0)
}
func TestReal(t *testing.T) {}
`})
	a := retainedBuildOK(t, n, "retained.test")
	for _, tc := range []struct {
		name, tail string
		run        bool
	}{
		{"fake-package-pass", "PASS", false},
		{"fake-package-fail", "FAIL", false},
		{"fake-test-pass", "--- PASS: TestFake (0.00s)", false},
		{"fake-test-fail", "--- FAIL: TestFake (0.00s)", false},
		{"framed-fake-pass", "\x16PASS", false},
		{"framed-fake-test", "\x16--- PASS: TestFake (0.00s)", false},
		{"completed-with-partial-diagnostic", "ordinary partial diagnostic", true},
		{"completed-with-report-shaped-tail", "--- FAIL: TestFake (0.00s)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc := *n
			nc.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "TAIL", tc.tail)
			if tc.run {
				nc.WitnessEnv = gotool.SetEnv(nc.WitnessEnv, "RUN", "yes")
			}
			r := retainedRuntimeFor(t, &nc, a, "retained.test", nil, nil)
			result := runRetained(t.Context(), t.Context(), &nc, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
			if result.ConversionErr != nil || result.BinaryState == nil || !result.BinaryState.Success() || result.HarnessComplete() != tc.run {
				t.Fatalf("EOF changed completion: %+v; stream=%+v", result, result.Stream)
			}
			wantTerminal, wantTests := "", 0
			if tc.run {
				wantTerminal, wantTests = "pass", 1
			}
			if result.HarnessTerminal != wantTerminal || len(result.Stream.tests) != wantTests || findTest(result.Stream.tests, "retained.test", "TestFake") != nil {
				t.Fatalf("diagnostic gained framing authority: %+v", result.Stream)
			}
			if !strings.HasSuffix(result.Stream.pkgOutput.b.String(), tc.tail) {
				t.Fatalf("partial diagnostic bytes changed: %q", result.Stream.pkgOutput.b.String())
			}
		})
	}
}

// The owned converter must deliver the final report and its complete diagnostic
// to the existing correlator, including exact input-buffer boundary fragments.
func TestRetainedConverterEOFBoundary(t *testing.T) {
	for _, size := range []int{4095, 4096, 8192} {
		input := "=== RUN   TestA\n--- FAIL: TestA (0.00s)\n" + strings.Repeat("x", size) + "\x16partial"
		for _, src := range []io.Reader{strings.NewReader(input), iotest.OneByteReader(strings.NewReader(input))} {
			var out bytes.Buffer
			if err := convertRetainedOutput(&out, src, "retained.test"); err != nil {
				t.Fatal(err)
			}
			st := parseTestStream("retained", "retained.test", &out, nil)
			if st.terminal != "" || len(st.tests) != 1 || st.tests[0].GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED {
				t.Fatalf("EOF outcome: %+v", st)
			}
			d := findDiagnostic(st.diags, "retained.test", "TestA")
			if d == nil || d.GetOutput() != input {
				t.Fatalf("size=%d lost or changed diagnostic: %v", size, d)
			}
		}
	}
}

func TestRetainedBinaryOwnsTerminationAndHarnessCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": `package p
import ("fmt"; "os"; "testing"; "strings")
func TestMain(m *testing.M) {
 switch os.Getenv("MODE") { case "silent": os.Exit(0); case "exit": os.Exit(7) }
 code:=m.Run()
 if os.Getenv("MODE")=="panic-after-fail" { panic("after FAIL") }
 os.Exit(code)
}
func TestPass(t *testing.T) {}
func TestLongOutput(t *testing.T) { fmt.Print(strings.Repeat("x",4096)) }
func TestFail(t *testing.T) { t.Fatal("ordinary failure") }
func TestPanic(t *testing.T) { panic("inside test") }
func TestNestedPanic(t *testing.T) { t.Run("child", func(t *testing.T) { panic("inside subtest") }) }
func FuzzReplay(f *testing.F) { f.Add("seed"); f.Fuzz(func(t *testing.T,s string){ if s!="seed" {t.Fatal(s)} }) }
func Example() { fmt.Println("example ran"); // Output: example ran
}
`})
	a := retainedBuildOK(t, n, "retained.test")
	for _, tc := range []struct {
		name, mode string
		selection  []string
		code       int
		complete   bool
		terminal   string
	}{
		{"pass", "", []string{"TestPass"}, 0, true, "pass"},
		{"long-output", "", []string{"TestLongOutput"}, 0, true, "pass"},
		{"fail", "", []string{"TestFail"}, 1, true, "fail"},
		{"panic", "", []string{"TestPanic"}, 2, false, ""},
		{"nested-panic", "", []string{"TestNestedPanic"}, 2, false, ""},
		{"panic-after-fail", "panic-after-fail", []string{"TestFail"}, 2, false, "fail"},
		{"silent", "silent", nil, 0, false, ""},
		{"exit", "exit", nil, 7, false, ""},
		{"example", "", []string{"Example"}, 0, true, "pass"},
		{"fuzz-seed", "", []string{"FuzzReplay"}, 0, true, "pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc := *n
			nc.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "MODE", tc.mode)
			runtime := retainedRuntimeFor(t, &nc, a, "retained.test", tc.selection, nil)
			p := &stipulatorv1.ProducerIdentity{}
			p.SetInvocation(n.Name)
			p.SetProcessOrdinal(3)
			result := runRetained(t.Context(), t.Context(), &nc, runtime, p, retainedProcessHooks{})
			if result.BinaryState == nil || result.BinaryState.ExitCode() != tc.code || result.ConversionErr != nil || result.HarnessComplete() != tc.complete || result.HarnessTerminal != tc.terminal {
				t.Fatalf("result=%+v; stream=%+v; wanted exit=%d completion=%t original=%s", result, result.Stream, tc.code, tc.complete, tc.terminal)
			}
			if p.GetProcessId() != int64(result.BinaryState.Pid()) {
				t.Fatal("producer is not the binary")
			}
			health := result.Classify(&nc, "retained.test")
			want := stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY
			if tc.code != 0 {
				want = stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED
			}
			if health.disposition != want {
				t.Fatalf("health=%v want %v; diags=%v", health.disposition, want, health.diags)
			}
			if tc.name == "panic" || tc.name == "nested-panic" {
				names := []string{tc.selection[0]}
				if tc.name == "nested-panic" {
					names = append(names, "TestNestedPanic/child")
				}
				for _, name := range names {
					tr := findTest(health.tests, "retained.test", name)
					if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED || !sameProcess(tr.GetProducer(), p) {
						t.Errorf("panic lost its attributed failure: %s: %v", name, tr)
					}
				}
				// Go attributes the panic dump to the parent after flushing
				// its nested child's report; preserve that toolchain attribution.
				name := names[0]
				d := findDiagnostic(health.diags, "retained.test", name)
				if d == nil || !strings.Contains(d.GetOutput(), "panic: inside") {
					t.Errorf("panic lost its diagnostic: %s: %v", name, d)
				}
			}
			if tc.name == "fuzz-seed" && findTest(result.Stream.tests, "retained.test", "FuzzReplay/seed#0") == nil {
				t.Fatal("committed seed did not execute")
			}
		})
	}
}

func TestRetainedRuntimeProfilesCoverageAndEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{
		"sub/p.go": "package sub\nfunc Answer() int { return 42 }\n",
		"sub/p_test.go": `package sub
import ("os"; "path/filepath"; "strings"; "testing")
func TestRuntime(t *testing.T) {
 wd,err:=os.Getwd(); if err!=nil{t.Fatal(err)}
 if os.Getenv("PWD")!=wd || filepath.Base(wd)!="sub" { t.Fatal("cwd/PWD",wd,os.Getenv("PWD")) }
 if !strings.HasPrefix(os.Getenv("PATH"),filepath.Join(os.Getenv("EXPECTED_GOROOT"),"bin")+string(os.PathListSeparator)) {t.Fatal("runtime PATH",os.Getenv("PATH"))}
 if os.Getenv("GOCOVERDIR")=="ambient-cover" || os.Getenv("GOCOVERDIR")=="" {t.Fatal("coverage directory not private")}
 if Answer()!=42{t.Fatal("answer")}
}
`,
	})
	snapshot, err := rootsRunner.TakeEnvSnapshot(t.Context(), n.Dir, witnessEnvOf(n))
	if err != nil {
		t.Fatal(err)
	}
	n.WitnessEnv = gotool.SetEnv(witnessEnvOf(n), "EXPECTED_GOROOT", snapshot.Value("GOROOT"))
	n.WitnessEnv = gotool.SetEnv(n.WitnessEnv, "GOCOVERDIR", "ambient-cover")
	retainedSetFlags(n, "-coverprofile=coverage.txt -memprofile=heap.out -blockprofile=block.out -mutexprofile=mutex.out -trace=trace.out")
	profile, err := openRetainedCoverage(n)
	if err != nil {
		t.Fatal(err)
	}
	a := retainedBuildOK(t, n, "retained.test/sub")
	r := retainedRuntimeFor(t, n, a, "retained.test/sub", nil, profile)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() {
		t.Fatalf("runtime: %+v; stream=%+v", result, result.Stream)
	}
	if err := profile.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"coverage.txt", "heap.out", "block.out", "mutex.out", "trace.out"} {
		info, err := os.Stat(filepath.Join(n.Dir, file))
		if err != nil || info.Size() == 0 {
			t.Errorf("invocation profile %s: %v", file, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(n.Dir, "coverage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "retained.test/sub/p.go:") || !strings.HasSuffix(strings.TrimSpace(string(data)), " 1") {
		t.Fatalf("coverage=%s", data)
	}
	// Compare the profile with cmd/go's own output for the same selected package.
	if _, err := ownedRunner.Run(t.Context(), n.Dir, witnessEnvOf(n), testCommandArgs(n, "retained.test/sub", nil, "")...); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(n.Dir, "coverage.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(want) {
		t.Fatalf("coverage differs from Go: retained=%s go=%s", data, want)
	}
}

func TestRetainedRawProfilePathsUseBinaryDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"sub/p_test.go": "package sub\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	n.Args = []string{"-test.memprofile=raw-heap.out"}
	a := retainedBuildOK(t, n, "retained.test/sub")
	r := retainedRuntimeFor(t, n, a, "retained.test/sub", nil, nil)
	result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{})
	if !result.HarnessComplete() {
		t.Fatalf("result=%+v stream=%+v", result, result.Stream)
	}
	if _, err := os.Stat(filepath.Join(n.Dir, "sub", "raw-heap.out")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(n.Dir, "raw-heap.out")); !os.IsNotExist(err) {
		t.Fatal("raw profile resolved at invocation cwd")
	}
}

func TestRetainedProcessAccountingCoversEverySpawnAndReap(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport \"testing\"\nfunc TestOK(t *testing.T){}\n"})
	var mu sync.Mutex
	live := map[int]string{}
	counts := map[string]int{}
	hooks := retainedProcessHooks{
		Spawned: func(stage string, pid int) {
			mu.Lock()
			defer mu.Unlock()
			if live[pid] != "" {
				t.Error("duplicate PID")
			}
			if len(live) != 0 {
				t.Error("one package owns overlapping stage roots")
			}
			live[pid] = stage
			counts[stage]++
		},
		Reaped: func(stage string, pid int, state *os.ProcessState) {
			mu.Lock()
			defer mu.Unlock()
			if live[pid] != stage || state == nil || state.Pid() != pid {
				t.Error("unregistered or unattributed reap")
			}
			delete(live, pid)
		},
	}
	build := buildRetained(t.Context(), n, "retained.test", hooks)
	if build.Err != nil {
		t.Fatal(build.Err)
	}
	defer build.Artifact.Close()
	r := retainedRuntimeFor(t, n, build.Artifact, "retained.test", nil, nil)
	for range 2 {
		if result := runRetained(t.Context(), t.Context(), n, r, &stipulatorv1.ProducerIdentity{}, hooks); !result.HarnessComplete() {
			t.Fatalf("attempt=%+v", result)
		}
	}
	if len(live) != 0 || len(counts) != 2 || counts["build"] != 1 || counts["binary"] != 2 {
		t.Fatalf("accounting live=%v counts=%v", live, counts)
	}
}

func TestRetainedCallerCancellationStopsBinaryAndConversion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes Go fixture packages")
	}
	neutralAmbient(t)
	n := retainedFixtureInvocation(t, map[string]string{"p_test.go": "package p\nimport (\"testing\";\"time\")\nfunc TestHang(t *testing.T){for{time.Sleep(time.Second)}}\n"})
	a := retainedBuildOK(t, n, "retained.test")
	r := retainedRuntimeFor(t, n, a, "retained.test", nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	start := time.Now()
	result := runRetained(ctx, ctx, n, r, &stipulatorv1.ProducerIdentity{}, retainedProcessHooks{Spawned: func(stage string, _ int) {
		if stage == "binary" {
			cancel()
		}
	}})
	if !result.Interrupted || result.HarnessComplete() || time.Since(start) > 5*time.Second || result.BinaryState == nil || result.Stream == nil {
		t.Fatalf("cancellation=%+v elapsed=%v", result, time.Since(start))
	}
}
