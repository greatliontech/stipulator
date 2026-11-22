package golang

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/gotool"
	"github.com/greatliontech/stipulator/stipulate"
)

// FuzzGoExecuteEventStream drives the stream classifier over adversarial
// `go test -json` bytes — malformed, truncated, reordered, duplicate, and
// post-abort event shapes among them — and pins the robustness contract:
// classification is a deterministic function of the bytes and exit,
// every recorded outcome is attributed to the producing process, an
// accepted (healthy) verdict exists only for a well-formed passing
// stream from a clean exit, and every refusal is loud — a degraded
// package always names its reason in a diagnostic.
func FuzzGoExecuteEventStream(f *testing.F) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCompletionExitHelper$")
	cmd.Env = append(os.Environ(), "STIPULATOR_COMPLETION_EXIT=1")
	failedExit := cmd.Run()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 {
		f.Fatalf("exit fixture did not produce status 1: %v", failedExit)
	}
	f.Add([]byte(`{"Action":"start","Package":"p"}`+"\n"+
		`{"Action":"run","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"output","Package":"p","Test":"TestA","Output":"stipulator:covers REQ-a-b\n"}`+"\n"+
		`{"Action":"pass","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	// Malformed bytes interleaved in the stream.
	f.Add([]byte(`{"Action":"start","Package":"p"}`+"\n"+"garbage line\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	// Truncated: started test, no terminal package event.
	f.Add([]byte(`{"Action":"run","Package":"p","Test":"TestA"}`+"\n"), true)
	// Reordered: outcome before its run event, terminal first.
	f.Add([]byte(`{"Action":"pass","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"run","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	// Duplicate outcomes for one test (a -count>1 stream shape).
	f.Add([]byte(`{"Action":"run","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"pass","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"run","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"pass","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	// Post-abort: events after the terminal package event.
	f.Add([]byte(`{"Action":"pass","Package":"p"}`+"\n"+
		`{"Action":"run","Package":"p","Test":"TestLate"}`+"\n"), false)
	// Abort output with a red exit.
	f.Add([]byte(`{"Action":"run","Package":"p","Test":"TestA"}`+"\n"+
		`{"Action":"output","Package":"p","Test":"TestA","Output":"panic: boom\n"}`+"\n"+
		`{"Action":"fail","Package":"p"}`+"\n"), true)
	// Abort output inside a green stream from a clean exit: suite health
	// accepts, observation completeness must not.
	f.Add([]byte(`{"Action":"output","Package":"p","Output":"panic: boom\n"}`+"\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	// CRLF line endings on every event.
	f.Add([]byte(`{"Action":"start","Package":"p"}`+"\r\n"+
		`{"Action":"pass","Package":"p"}`+"\r\n"), false)
	// One event far beyond typical line-buffer sizes.
	f.Add([]byte(`{"Action":"output","Package":"p","Output":"`+strings.Repeat("A", 70_000)+`"}`+"\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	// Events naming a foreign package interleaved with the run's own.
	f.Add([]byte(`{"Action":"start","Package":"q"}`+"\n"+
		`{"Action":"pass","Package":"q"}`+"\n"+
		`{"Action":"pass","Package":"p"}`+"\n"), false)
	f.Add([]byte(""), true)
	// Ordinary assertion failure: every runnable finished and the package
	// failed normally, which establishes completion but never passing health.
	f.Add([]byte(`{"Action":"run","Test":"TestA"}`+"\n"+
		`{"Action":"fail","Test":"TestA"}`+"\n"+
		`{"Action":"fail"}`+"\n"), true)

	f.Fuzz(func(t *testing.T, data []byte, exitFail bool) {
		stipulate.Covers(t, "REQ-policy-attribution", "REQ-go-policy-complete")
		producer := &stipulatorv1.ProducerIdentity{}
		producer.SetInvocation("fuzz")
		producer.SetProcessId(1)
		producer.SetProcessOrdinal(1)
		var waitErr error
		if exitFail {
			waitErr = failedExit
		}
		parse := func() (*streamState, packageRun) {
			st := parseTestStream("fuzz", "example.com/p", bytes.NewReader(data), producer)
			return st, classifyRun("fuzz", "example.com/p", st, waitErr, &boundedBuffer{}, "")
		}
		st1, run1 := parse()
		st2, run2 := parse()

		// Deterministic attribution: the same bytes classify identically.
		if run1.disposition != run2.disposition {
			t.Fatalf("nondeterministic disposition: %v vs %v", run1.disposition, run2.disposition)
		}
		if len(run1.tests) != len(run2.tests) {
			t.Fatalf("nondeterministic outcome count: %d vs %d", len(run1.tests), len(run2.tests))
		}
		for i := range run1.tests {
			if !proto.Equal(run1.tests[i], run2.tests[i]) {
				t.Fatalf("nondeterministic outcome %d: %v vs %v", i, run1.tests[i], run2.tests[i])
			}
		}
		if r1, r2 := incompleteObservationReason(st1, waitErr, run1.disposition), incompleteObservationReason(st2, waitErr, run2.disposition); r1 != r2 {
			t.Fatalf("nondeterministic observation completeness: %q vs %q", r1, r2)
		}

		// A classified run always reaches a terminal fact.
		if run1.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_UNSPECIFIED {
			t.Fatal("classifier left a run without a terminal disposition")
		}
		// Every outcome is bound to the producing process.
		for _, tr := range run1.tests {
			if tr.GetProducer().GetInvocation() != "fuzz" {
				t.Fatalf("outcome escaped producer attribution: %v", tr)
			}
		}
		// Acceptance is earned: a healthy verdict implies a well-formed
		// passing stream and a clean exit.
		if run1.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
			if exitFail || st1.malformed != "" || st1.postTerminal || st1.events == 0 ||
				(st1.terminal != "pass" && st1.terminal != "skip") {
				t.Fatalf("ill-formed stream accepted as healthy: terminal=%q malformed=%q post=%v events=%d exitFail=%v",
					st1.terminal, st1.malformed, st1.postTerminal, st1.events, exitFail)
			}
		}
		// Refusals are loud: a degraded package names its reason.
		if run1.disposition == stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
			found := false
			for _, d := range run1.diags {
				if d.GetTest() == "" && d.GetOutput() != "" {
					found = true
				}
			}
			if !found {
				t.Fatal("degraded run carries no package diagnostic naming the refusal")
			}
		}
		// A completed-eligible classification never coexists with abort
		// residue: eligibility implies nothing started stayed unfinished.
		if incompleteObservationReason(st1, waitErr, run1.disposition) == "" {
			if len(startedTests(st1)) != 0 || st1.sawAbort || (st1.terminal != "pass" && st1.terminal != "fail") {
				t.Fatal("observation eligibility granted over an unproven testlog flush")
			}
			if st1.terminal == "fail" {
				failed := false
				for _, test := range st1.tests {
					failed = failed || test.GetOutcome() == stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED
				}
				if !exitFail || !failed {
					t.Fatal("completion granted to an unexplained failure")
				}
			}
		}
	})
}

func TestCompletionExitHelper(t *testing.T) {
	if os.Getenv("STIPULATOR_COMPLETION_EXIT") == "1" {
		os.Exit(1)
	}
}

// FuzzGoExecuteTestlogIngestion drives gofresh testlog ingestion over
// adversarial testlog bytes — truncated, duplicate, reordered, and
// garbage lines among them — under the pre-spawn observation bracket the
// executor now always supplies, and pins the executor's contract with it:
// ingestion of the same bytes against one bracket is deterministic in the
// manifest it derives, a successful ingestion yields sealed completed
// evidence whose manifest decodes, a refused ingestion surfaces an error
// the executor turns into a loud incomplete observation, and a bracketed
// root mutated inside the capture-to-ingest window seals the observation
// unverifiable — never bound, whatever the log bytes claim.
func FuzzGoExecuteTestlogIngestion(f *testing.F) {
	f.Add([]byte("# test log\ngetenv HOME\nopen testdata/fixture.txt\n"), false)
	f.Add([]byte("open a.txt\nopen a.txt\nstat b.txt\nchdir sub\nopen c.txt\n"), false)
	// Truncated mid-line: a mid-write kill's residue.
	f.Add([]byte("# test log\nopen testdata/fixt"), false)
	// Reordered header and unknown ops.
	f.Add([]byte("open x\n# test log\nfrobnicate y\n"), false)
	f.Add([]byte("\x00\xff garbage\n"), false)
	f.Add([]byte(""), false)
	// The run-to-ingest window: the bracketed root moves after capture,
	// before ingestion — a clean covered read and an empty log alike must
	// seal unverifiable.
	f.Add([]byte("# test log\nopen fixture.txt\n"), true)
	f.Add([]byte(""), true)

	baseDir := f.TempDir()
	pkgDir := filepath.Join(baseDir, "p")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		f.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "go.mod"), []byte("module example.com/fuzzingest\n\ngo 1.26\n"), 0o644); err != nil {
		f.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "p_test.go"), []byte("package p\n\nimport \"testing\"\n\nfunc TestP(*testing.T) {}\n"), 0o644); err != nil {
		f.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture.txt"), []byte("recorded"), 0o644); err != nil {
		f.Fatal(err)
	}
	buildEnv, err := gotool.NormalizeEnv(os.Environ())
	if err != nil {
		f.Fatal(err)
	}
	sess, err := gofresh.NewSession(context.Background(), gofresh.BuildDescription{
		Directory:   baseDir,
		Environment: buildEnv,
		Inputs:      []string{},
	}, gofresh.SessionPolicy{
		EvidenceRoot:   baseDir,
		Execution:      gofresh.PackageExecution,
		PrepareCommand: ownedRunner.PrepareCommand,
	})
	if err != nil {
		f.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/fuzzingest/p", Symbol: "TestP"}
	snap, err := sess.Snapshot(context.Background(), []gofresh.Subject{subject}, gofresh.CodeResult)
	if err != nil {
		f.Fatal(err)
	}
	built, err := snap.BindBuilt("example.com/fuzzingest/p", "fuzz-bin")
	if err != nil {
		f.Fatal(err)
	}
	runEnv := gotool.SetEnv(gotool.SetEnv(buildEnv, "HOME", "/nonexistent"), "PWD", pkgDir)
	rb, err := snap.BindRuntime(context.Background(), gofresh.RuntimeDescription{Directory: pkgDir, Environment: runEnv})
	if err != nil {
		f.Fatal(err)
	}
	movedPath := filepath.Join(pkgDir, "moved.txt")

	f.Fuzz(func(t *testing.T, log []byte, mutate bool) {
		stipulate.Covers(t, "REQ-policy-attribution", "REQ-evidence-witness-freshness-bracket")
		_ = os.Remove(movedPath)
		t.Cleanup(func() { _ = os.Remove(movedPath) })

		logDir := t.TempDir()
		logPath1 := filepath.Join(logDir, "log1.txt")
		logPath2 := filepath.Join(logDir, "log2.txt")
		if err := os.WriteFile(logPath1, log, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(logPath2, log, 0o600); err != nil {
			t.Fatal(err)
		}

		openUnit := func(logPath, id string) (*gofresh.Unit, gofresh.Contribution) {
			t.Helper()
			u, err := rb.BeginIdentity(subject)
			if err != nil {
				t.Fatal(err)
			}
			contrib, err := u.Register(context.Background(), built, gofresh.ProcessDescription{
				Identity:      id,
				Capture:       logPath,
				BracketPaths:  []string{"p"},
				ExcludedPaths: []string{".", ".git"},
			})
			if err != nil {
				t.Fatal(err)
			}
			return u, contrib
		}
		u1, c1 := openUnit(logPath1, "fuzz#1:example.com/p")
		defer u1.Abort()
		u2, c2 := openUnit(logPath2, "fuzz#1:example.com/p")
		defer u2.Abort()

		if mutate {
			// The mutation lands inside the capture-to-ingest window and
			// persists: the bracket must move, whatever the log carries.
			if err := os.WriteFile(movedPath, []byte("moved"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		if err := u1.Submit(c1, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
			t.Fatal(err)
		}
		if err := u2.Submit(c2, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
			t.Fatal(err)
		}
		_, err1 := u1.Close(context.Background())
		_, err2 := u2.Close(context.Background())
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("nondeterministic ingestion: %v vs %v", err1, err2)
		}
		a1, _ := u1.Account()
		a2, _ := u2.Account()
		var spanErr1 *gofresh.RuntimeInputSpanError
		if err1 != nil && !errors.As(err1, &spanErr1) {
			if err1.Error() == "" {
				t.Fatal("ingestion refused silently")
			}
			return
		}
		if a1.Inputs.Manifest == "" || a2.Inputs.Manifest == "" {
			t.Fatalf("accepted ingestion missing finalized manifest: %+v / %+v", a1, a2)
		}
		if a1.Inputs.Manifest != a2.Inputs.Manifest {
			t.Fatal("nondeterministic manifest from identical testlog bytes")
		}
		if _, err := gofresh.ModuleRelRuntimeInputPaths(a1.Inputs.Manifest); err != nil {
			t.Fatalf("accepted manifest does not decode: %v", err)
		}
		if mutate && !a1.Inputs.Unverifiable && !a1.Inputs.BracketMoved {
			t.Fatalf("a bracketed root mutated inside the capture-to-ingest window closed verifiable: err=%v acct=%+v", err1, a1)
		}
	})
}
