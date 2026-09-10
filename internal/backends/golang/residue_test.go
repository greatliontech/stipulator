package golang

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// residueStream is a go test -json stream that leaves every kind of
// residue: package output, a started test with output and no terminal
// event, and an unparseable tail.
const residueStream = `{"Action":"start","Package":"example.com/x"}
{"Action":"output","Package":"example.com/x","Output":"package-level line\n"}
{"Action":"run","Package":"example.com/x","Test":"TestHang"}
{"Action":"output","Package":"example.com/x","Test":"TestHang","Output":"=== RUN   TestHang\n"}
{"Action":"output","Package":"example.com/x","Test":"TestHang","Output":"hang residue\n"}
{"Action":"fail","Package":"example.com/x","Elapsed":1}
not json at all
`

// sectionsInOrder reports whether every marker appears in the text, each
// after the previous.
func sectionsInOrder(text string, markers ...string) bool {
	at := -1
	for _, m := range markers {
		i := strings.Index(text, m)
		if i < 0 || i <= at {
			return false
		}
		at = i
	}
	return true
}

// What a run left behind is one composition in one order on every
// diagnostic that carries it — the cut-off diagnostic, the degrade
// diagnostic (the silent stream's included), the terminal-fail
// diagnostic (its deadline head first: REQ-policy-budget-attribution
// names the bound and the denied subjects before the residue; a
// started test with no output is the head's to name, never sectioned)
// — with truncation propagated from every buffer, and the unparsed
// remainder rendered as a bounded share so it never displaces the
// package output.
//
//gofresh:pure
func TestRunResidueIsOneCompositionOnEveryArm(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-budget-attribution")
	fresh := func() (*streamState, *boundedBuffer) {
		st := parseTestStream("inv", "example.com/x", strings.NewReader(residueStream), nil)
		stderr := &boundedBuffer{}
		stderr.write("goroutine dump\n")
		return st, stderr
	}
	remainder := []string{"malformed stream: ", "not json at all"}
	// The clean-stream arms carry no malformed section.
	clean := []string{"package output:\n", "package-level line", "--- aborted: TestHang ---\n", "hang residue", "stderr:\n", "goroutine dump", "process exit: exit status 2"}
	order := append(append([]string{}, remainder...), clean...)

	st, stderr := fresh()
	out := runResidue(st, stderr, errors.New("exit status 2"), "HEAD LINE")
	if !sectionsInOrder(out.b.String(), append([]string{"HEAD LINE"}, order...)...) || !strings.HasPrefix(out.b.String(), "HEAD LINE\nmalformed stream: ") {
		t.Fatalf("residue with a head:\n%s", out.b.String())
	}
	if out.truncated {
		t.Fatal("nothing was truncated")
	}
	// No head: the first section opens the text, and a truncated
	// aborted-test buffer marks the whole.
	st, stderr = fresh()
	st.perTest["TestHang"].truncated = true
	out = runResidue(st, stderr, nil, "")
	if !strings.HasPrefix(out.b.String(), "malformed stream: ") || strings.Contains(out.b.String(), "process exit") || !out.truncated {
		t.Fatalf("headless residue (truncated %v):\n%s", out.truncated, out.b.String())
	}

	// The degrade arm: the malformed tail degrades the run; its
	// diagnostic is the reason, then the same residue — the aborted
	// test included.
	st, stderr = fresh()
	run := classifyRun("inv", "example.com/x", st, errors.New("exit status 2"), stderr, "")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED || len(run.diags) != 1 {
		t.Fatalf("degrade arm: %v %d", run.disposition, len(run.diags))
	}
	if got := run.diags[0].GetOutput(); !sectionsInOrder(got, append([]string{"unparseable output"}, order...)...) {
		t.Fatalf("degrade diagnostic:\n%s", got)
	}

	// A started test that left no output is not sectioned: the head's
	// roster attributes the denied subjects, the residue carries output.
	st = parseTestStream("inv", "example.com/x", strings.NewReader(`{"Action":"run","Package":"example.com/x","Test":"TestSilent"}`+"\n"), nil)
	if out = runResidue(st, &boundedBuffer{}, nil, ""); strings.Contains(out.b.String(), "aborted") {
		t.Fatalf("an output-less started test was sectioned:\n%s", out.b.String())
	}
	// A cap-sized remainder renders as its bounded share, marked, and
	// leaves the package output — a poisoned build stream's compiler
	// diagnostic — its room.
	// A build failure's output runs to kilobytes: its last line must
	// survive beside the bounded remainder.
	var compiler strings.Builder
	for i := 0; i < 400; i++ {
		compiler.WriteString(`{"Action":"build-output","ImportPath":"example.com/x","Output":"x_test.go:` + strconv.Itoa(i) + `: compiler diagnostic line of ordinary length here\n"}` + "\n")
	}
	compiler.WriteString(`{"Action":"build-output","ImportPath":"example.com/x","Output":"COMPILER-LAST-LINE\n"}` + "\n")
	poisonedBuild := compiler.String() + strings.Repeat("x", failureOutputCap)
	st = parseTestStream("inv", "example.com/x", strings.NewReader(poisonedBuild), nil)
	out = runResidue(st, &boundedBuffer{}, nil, "")
	if !out.truncated || !strings.Contains(out.b.String(), "COMPILER-LAST-LINE") {
		t.Fatalf("a cap-sized remainder displaced the package output or went unmarked (truncated %v, %d bytes):\n%.200s", out.truncated, out.b.Len(), out.b.String())
	}
	// The share's cut never splits a character: a multibyte poison
	// positioned so the share lands mid-rune still renders valid text.
	poison := "ab" + strings.Repeat("日", remainderShare)
	st = parseTestStream("inv", "example.com/x", strings.NewReader(poison), nil)
	out = runResidue(st, &boundedBuffer{}, nil, "")
	if !out.truncated || !utf8.ValidString(out.b.String()) {
		t.Fatalf("the share cut split a rune (truncated %v, valid %v)", out.truncated, utf8.ValidString(out.b.String()))
	}
	// Raw bytes in the remainder and on stderr are scrubbed: the
	// diagnostic rides a UTF-8-validated proto field, so a garbled byte
	// must never turn a verdict into a serialization fault.
	st = parseTestStream("inv", "example.com/x", strings.NewReader("\xff\xfe raw linker bytes\n"), nil)
	stderr = &boundedBuffer{}
	stderr.write("\xfe raw stderr\n")
	run = classifyRun("inv", "example.com/x", st, errors.New("exit status 2"), stderr, "")
	if got := run.diags[0].GetOutput(); !utf8.ValidString(got) || !strings.Contains(got, "raw linker bytes") || !strings.Contains(got, "raw stderr") {
		t.Fatalf("raw bytes reached the diagnostic unscrubbed or scrubbing lost the text (valid %v):\n%q", utf8.ValidString(got), got)
	}
	// Scrubbing widens a lone high byte into a three-byte rune: a
	// remainder below the share raw and above it scrubbed is cut, and
	// the mark measures the text that was cut.
	st = parseTestStream("inv", "example.com/x", strings.NewReader(strings.Repeat("\xe9 ", remainderShare/4)), nil)
	out = runResidue(st, &boundedBuffer{}, nil, "")
	if !out.truncated || !utf8.ValidString(out.b.String()) {
		t.Fatalf("a scrub-widened remainder was cut without its mark (truncated %v, valid %v, %d bytes)", out.truncated, utf8.ValidString(out.b.String()), out.b.Len())
	}
	// The silent-stream degrade arm: the reason, then what the process
	// left — stderr and its exit.
	st = parseTestStream("inv", "example.com/x", strings.NewReader(""), nil)
	stderr = &boundedBuffer{}
	stderr.write("goroutine dump\n")
	run = classifyRun("inv", "example.com/x", st, errors.New("exit status 2"), stderr, "")
	if got := run.diags[0].GetOutput(); run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED || !sectionsInOrder(got, "produced no events", "stderr:\n", "goroutine dump", "process exit: exit status 2") {
		t.Fatalf("silent-stream diagnostic (%v):\n%s", run.disposition, got)
	}
	// The cut-off arm end to end: the envelope head and its
	// started-but-unfinished names, then the residue.
	st, stderr = fresh()
	cut := packageRun{pkg: "example.com/x", aborted: startedTests(st), residue: runResidue(st, stderr, nil, "")}
	if err := finalizeRun(&NormalizedInvocation{Name: "inv", Timeout: 90 * time.Second}, &cut, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := cut.diags[0].GetOutput(); !sectionsInOrder(got, "invocation timeout 1m30s expired before the package completed", "started but unfinished: TestHang", "malformed stream: ", "package output:\n", "--- aborted: TestHang ---\n", "stderr:\n", "goroutine dump") || strings.Contains(got, "process exit") {
		t.Fatalf("cut-off diagnostic:\n%s", got)
	}

	// The terminal-fail arm over a clean stream: the residue after the
	// deadline head, the process exit closing it.
	cleanStream := strings.TrimSuffix(residueStream, "not json at all\n")
	st = parseTestStream("inv", "example.com/x", strings.NewReader(cleanStream), nil)
	st.binaryTimeout = "300ms"
	stderr = &boundedBuffer{}
	stderr.write("goroutine dump\n")
	run = classifyRun("inv", "example.com/x", st, errors.New("exit status 2"), stderr, "300ms")
	if run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT || len(run.diags) != 1 {
		t.Fatalf("terminal-fail arm: %v %d", run.disposition, len(run.diags))
	}
	got := run.diags[0].GetOutput()
	if !sectionsInOrder(got, append([]string{"test binary timeout 300ms exhausted", "running when the budget expired: TestHang"}, clean...)...) || strings.Contains(got, "malformed") {
		t.Fatalf("terminal-fail diagnostic:\n%s", got)
	}
	// A plain failure carries no head: the residue opens the diagnostic.
	st = parseTestStream("inv", "example.com/x", strings.NewReader(cleanStream), nil)
	run = classifyRun("inv", "example.com/x", st, errors.New("exit status 1"), &boundedBuffer{}, "")
	if got := run.diags[0].GetOutput(); run.disposition != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED || !strings.HasPrefix(got, "package output:\n") || !sectionsInOrder(got, "package output:\n", "--- aborted: TestHang ---\n", "process exit: exit status 1") {
		t.Fatalf("plain failure diagnostic (%v):\n%s", run.disposition, got)
	}
}
