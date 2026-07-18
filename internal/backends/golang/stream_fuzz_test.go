package golang

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
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

	f.Fuzz(func(t *testing.T, data []byte, exitFail bool) {
		stipulate.Covers(t, "REQ-policy-attribution", "REQ-go-policy-complete")
		producer := &stipulatorv1.ProducerIdentity{}
		producer.SetInvocation("fuzz")
		producer.SetProcessId(1)
		producer.SetProcessOrdinal(1)
		var waitErr error
		if exitFail {
			waitErr = errors.New("exit status 1")
		}
		parse := func() (*streamState, packageRun) {
			st := parseTestStream("fuzz", "example.com/p", bytes.NewReader(data), producer)
			return st, classifyRun("fuzz", "example.com/p", st, waitErr, &boundedBuffer{})
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
		if r1, r2 := incompleteObservationReason(st1, waitErr, run1.disposition, "/tmp/log"), incompleteObservationReason(st2, waitErr, run2.disposition, "/tmp/log"); r1 != r2 {
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
		if incompleteObservationReason(st1, waitErr, run1.disposition, "/tmp/log") == "" {
			if len(startedTests(st1)) != 0 || st1.sawAbort || st1.terminal != "pass" {
				t.Fatal("observation eligibility granted over an unproven testlog flush")
			}
		}
	})
}

// FuzzGoExecuteTestlogIngestion drives gofresh testlog ingestion over
// adversarial testlog bytes — truncated, duplicate, reordered, and
// garbage lines among them — and pins the executor's contract with it:
// ingestion of the same bytes is deterministic in the manifest it
// derives, a successful ingestion yields sealed completed evidence whose
// manifest decodes, and a refused ingestion surfaces an error the
// executor turns into a loud incomplete observation, never a silent
// completed one.
func FuzzGoExecuteTestlogIngestion(f *testing.F) {
	f.Add([]byte("# test log\ngetenv HOME\nopen testdata/fixture.txt\n"))
	f.Add([]byte("open a.txt\nopen a.txt\nstat b.txt\nchdir sub\nopen c.txt\n"))
	// Truncated mid-line: a mid-write kill's residue.
	f.Add([]byte("# test log\nopen testdata/fixt"))
	// Reordered header and unknown ops.
	f.Add([]byte("open x\n# test log\nfrobnicate y\n"))
	f.Add([]byte("\x00\xff garbage\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, log []byte) {
		stipulate.Covers(t, "REQ-policy-attribution")
		dir := t.TempDir()
		pkgDir := filepath.Join(dir, "p")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		env := []string{"HOME=/nonexistent", "PATH=/usr/bin"}
		ingest := func() (runtimeinput.Observation, error) {
			return runtimeinput.FromTestLogEnv(log, dir, pkgDir, env,
				runtimeinput.WithCompletedProcess("fuzz#1:example.com/p"),
				runtimeinput.WithExcludedPaths(".", ".git"))
		}
		o1, err1 := ingest()
		o2, err2 := ingest()
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("nondeterministic ingestion: %v vs %v", err1, err2)
		}
		if err1 != nil {
			// The executor maps this to an incomplete observation whose
			// reason carries the error — it must say something.
			if err1.Error() == "" {
				t.Fatal("ingestion refused silently")
			}
			return
		}
		s1, err := runtimeinput.CompletedState(o1)
		if err != nil {
			t.Fatalf("accepted ingestion is not sealed completed evidence: %v", err)
		}
		s2, err := runtimeinput.CompletedState(o2)
		if err != nil {
			t.Fatal(err)
		}
		// The manifest is a pure function of the bytes and environment;
		// only content digests may move between calls.
		if s1.Manifest != s2.Manifest {
			t.Fatal("nondeterministic manifest from identical testlog bytes")
		}
		if _, err := runtimeinput.ModuleRelPaths(s1.Manifest); err != nil {
			t.Fatalf("accepted manifest does not decode: %v", err)
		}
	})
}
