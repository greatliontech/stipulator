package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestRunTestsFreshDegrades pins REQ-evidence-freshness-degrade: a fault on
// the freshness path (here: a module that enumerates no runnable tests)
// falls back to the full witnessing run instead of failing the caller, and
// the result names the fault.
func TestRunTestsFreshDegrades(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-freshness-degrade")
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/empty\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatalf("freshness fault did not degrade to the full run: %v", err)
	}
	if len(tr.Outcomes) != 0 {
		t.Fatalf("empty module produced outcomes: %v", tr.Outcomes)
	}
	if tr.Degraded == "" {
		t.Fatal("degraded run did not name its fault")
	}
}

// fresh fails the calling phase when the freshness path silently fell back
// to the full run: a degraded run exercises nothing this test pins, and the
// fault text is the difference between a contract violation and an
// environmental fault.
func fresh(t *testing.T, tr *verify.TestRun, phase string) {
	t.Helper()
	if tr.Degraded != "" {
		t.Fatalf("%s: freshness path degraded: %s", phase, tr.Degraded)
	}
}

func sameRegistrationSet(a, b []verify.Registration) bool {
	set := func(rs []verify.Registration) map[verify.Registration]bool {
		m := map[verify.Registration]bool{}
		for _, r := range rs {
			m[r] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	if len(sa) != len(sb) {
		return false
	}
	for r := range sa {
		if !sb[r] {
			return false
		}
	}
	return true
}

// TestRunTestsFresh pins the freshness-aware witness run
// (REQ-evidence-witness-freshness): the first run executes and fingerprints
// everything, retrying past the aborting fixture to unshadow its sibling;
// the second serves from the cache by proven equivalence with identical
// outcomes and registrations; a source edit re-stales exactly the affected
// closure; steady state re-runs only the aborter; a fixture content change
// re-runs its reader through the manifest guard; and the cache lands
// gitignored.
//
// The test copies its fixture module before running it, so every fixture
// file rides this process's testlog manifest; the child go invocations see
// only those copies, and the toolchain itself is pinned by the
// fingerprint's toolchain guard. That is why the purity assertion below is
// sound.
//
//gofresh:pure
func TestRunTestsFresh(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("runs go test per package")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("testdata/fixturemod")); err != nil {
		t.Fatal(err)
	}

	first, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, first, "first run")
	if first.Fresh != 0 || first.Ran == 0 {
		t.Fatalf("first run: ran=%d fresh=%d, want everything ran", first.Ran, first.Fresh)
	}
	if first.Outcomes["example.com/fixture/lib.TestAdd"] != verify.TestPassed {
		t.Fatalf("TestAdd outcome missing: %v", first.Outcomes)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".stipulator", "cache", "witnesses.json")); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".stipulator", "cache", ".gitignore")); err != nil {
		t.Fatalf("cache not ignored: %v", err)
	}

	// The aborting invocation retries its incomplete remainder, so the
	// shadowed sibling is unshadowed within the first run already.
	if first.Outcomes["example.com/fixture/panicky.TestShadowed"] != verify.TestPassed {
		t.Fatalf("the shadowed test was not unshadowed by a retry invocation: %v", first.Outcomes["example.com/fixture/panicky.TestShadowed"])
	}

	second, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, second, "second run")
	if second.Fresh == 0 {
		t.Fatalf("second run served nothing: ran=%d fresh=%d", second.Ran, second.Fresh)
	}
	if second.Outcomes["example.com/fixture/lib.TestAdd"] != verify.TestPassed {
		t.Fatalf("served outcome lost: %v", second.Outcomes["example.com/fixture/lib.TestAdd"])
	}
	// Served registrations are the recorded ones — the same set the first
	// run produced, no losses, no fabrications.
	if !sameRegistrationSet(first.Registrations, second.Registrations) {
		t.Fatalf("registration sets differ:\nfirst:  %+v\nsecond: %+v", first.Registrations, second.Registrations)
	}

	// Every first-run outcome survives the second run.
	for k, v := range first.Outcomes {
		if second.Outcomes[k] != v {
			t.Fatalf("outcome %s changed or vanished: %v -> %v", k, v, second.Outcomes[k])
		}
	}

	// An edit inside Add's closure re-stales TestAdd; an untouched package's
	// pure tests stay served.
	libPath := filepath.Join(tmp, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), "return a + b", "return b + a", 1)
	if edited == string(src) {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(libPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, third, "edit run")
	if third.Ran == 0 {
		t.Fatal("a closure edit re-ran nothing")
	}
	if third.Fresh == 0 && second.Fresh > 1 {
		t.Fatalf("an edit in one package re-ran the world: ran=%d fresh=%d", third.Ran, third.Fresh)
	}
	if third.Outcomes["example.com/fixture/lib.TestAdd"] != verify.TestPassed {
		t.Fatalf("edited test not re-witnessed: %v", third.Outcomes)
	}

	// The purity + runtime-input path: a //gofresh:pure fixture reader
	// serves while its fixture is unchanged (purity suppresses the file-IO
	// unverifiability; the manifest guard still applies) and re-runs the
	// moment the fixture's content moves — the guard, not the closure,
	// catches a non-source change.
	fourth, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, fourth, "steady state")
	// Steady state re-runs exactly the aborting test: its invocation dies
	// before the testlog flush, so its evidence is never cacheable — an
	// absent manifest would read as gofresh's "no runtime inputs observed"
	// assertion, which an abort never earns.
	if fourth.Ran != 1 {
		t.Fatalf("steady state ran %d tests, want exactly the aborting one", fourth.Ran)
	}
	if fourth.Outcomes["example.com/fixture/panicky.TestPanics"] != verify.TestFailed {
		t.Fatalf("the aborting test did not re-run red: %v", fourth.Outcomes["example.com/fixture/panicky.TestPanics"])
	}
	if fourth.Outcomes["example.com/fixture/freader.TestReadsFixture"] != verify.TestPassed {
		t.Fatalf("pure fixture reader not served: %v", fourth.Outcomes["example.com/fixture/freader.TestReadsFixture"])
	}
	if err := os.WriteFile(filepath.Join(tmp, "freader", "data.txt"), []byte("seed-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fifth, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh(t, fifth, "fixture-change run")
	if fifth.Ran < 2 {
		t.Fatal("a fixture content change served from cache: the runtime-input guard did not stale")
	}
	if fifth.Outcomes["example.com/fixture/freader.TestReadsFixture"] != verify.TestPassed {
		t.Fatalf("fixture reader not re-witnessed: %v", fifth.Outcomes)
	}
}
