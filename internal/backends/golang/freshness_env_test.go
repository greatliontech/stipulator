package golang

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// simpleModule writes a self-contained module with one passing test
// that also reads its module root listing and a .git file — the
// volatile observations the witness path must never record.
func simpleModule(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/envfix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real (empty) repository: go's VCS stamping probes it, and the
	// fixture test reads it — the volatile observation under test.
	git := exec.Command("git", "init", "-q", tmp)
	if out, err := git.CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v (%s)", err, out)
	}
	testSource := `package envfix

import (
	"os"
	"testing"
)

func TestReadsVolatileState(t *testing.T) {
	if _, err := os.ReadDir("."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(".git/HEAD"); err != nil {
		t.Fatal(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "envfix_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// TestRunTestsFreshUnderForeignWorkspace pins the environment seam
// that made freshness witnesses fail only inside a completed gate:
// the witness runner pins GOWORK per module, and the analysis engine
// must analyze under the same pinning — an ambient workspace pointing
// at another tree (the outer harness's own, when this run is itself a
// witness) must never leak into the module's go invocations.
func TestRunTestsFreshUnderForeignWorkspace(t *testing.T) {
	work, err := filepath.Abs("../../../go.work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(work); err != nil {
		t.Skipf("no outer workspace to leak: %v", err)
	}
	t.Setenv("GOWORK", work)

	tmp := simpleModule(t)
	tr, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Degraded != "" {
		t.Fatalf("freshness path degraded under foreign GOWORK: %s", tr.Degraded)
	}
	if tr.Outcomes["example.com/envfix.TestReadsVolatileState"] == 0 {
		t.Fatalf("fixture test outcome missing: %v", tr.Outcomes)
	}
	// The fixture's file-I/O closure is unverifiable, so its record
	// cannot publish: the shrinkage must be visible as a number.
	if tr.Uncached != tr.Ran || tr.Uncached == 0 {
		t.Fatalf("uncached = %d with ran = %d; cache shrinkage must be counted", tr.Uncached, tr.Ran)
	}
}

// TestFreshRunCarriesFailureOutput pins the shard merge of failure
// diagnostics: a red witness must be diagnosable from the run that
// saw it, through the concurrent path.
func TestFreshRunCarriesFailureOutput(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/redfix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := `package redfix

import "testing"

func TestAlwaysRed(t *testing.T) {
	t.Fatal("the diagnostic that must survive the merge")
}
`
	if err := os.WriteFile(filepath.Join(tmp, "redfix_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	key := "example.com/redfix.TestAlwaysRed"
	if tr.Outcomes[key] != 2 {
		t.Fatalf("outcome = %v, want failed", tr.Outcomes[key])
	}
	if !strings.Contains(tr.Failures[key], "the diagnostic that must survive the merge") {
		t.Fatalf("failure diagnostics lost in the merge: %q", tr.Failures[key])
	}
}
