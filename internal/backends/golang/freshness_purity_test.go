package golang

import (
	"os"
	"path/filepath"
	"testing"
)

// pureReaderModule writes a self-contained module whose one test reads a
// data file — an unverifiable file dependence — and carries the
// //gofresh:pure directive, the author's in-source assertion that the
// read is behavior-irrelevant beyond what the runtime-input digest
// already guards (REQ-evidence-witness-freshness: "asserts purity in
// source, the deliberate opt-in").
func pureReaderModule(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/purefix\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSource := `package purefix

import (
	"os"
	"testing"
)

//gofresh:pure
func TestReadsAssertedFixture(t *testing.T) {
	if _, err := os.ReadFile("data.txt"); err != nil {
		t.Fatal(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "purefix_test.go"), []byte(testSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// TestPurityDirectivePublishesAndServes pins the deliberate opt-in end to
// end: a file-reading test with a //gofresh:pure source directive
// publishes its witness record on the first run and is served from the
// cache — verification by proven equivalence — on the second.
//
//gofresh:pure
func TestPurityDirectivePublishesAndServes(t *testing.T) {
	tmp := pureReaderModule(t)

	first, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("first run degraded: %s", first.Degraded)
	}
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run: ran=%d uncached=%d; the directive-pure record must publish", first.Ran, first.Uncached)
	}

	second, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("second run degraded: %s", second.Degraded)
	}
	if second.Fresh != 1 || second.Ran != 0 {
		t.Fatalf("second run: fresh=%d ran=%d; the published record must serve", second.Fresh, second.Ran)
	}
	if second.Outcomes["example.com/purefix.TestReadsAssertedFixture"] == 0 {
		t.Fatalf("served outcome missing: %v", second.Outcomes)
	}
}

// TestPurityNeverWaivesInputDigest pins the boundary of the assertion:
// purity suppresses unverifiability only — every hashable guard stays
// active (gofresh REQ-purity-override), so a change to the observed data
// file stales the record and the test re-runs.
//
//gofresh:pure
func TestPurityNeverWaivesInputDigest(t *testing.T) {
	tmp := pureReaderModule(t)

	first, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// The staling assertion below is meaningful only if the first run
	// actually published: stale-because-never-cached would pass vacuously.
	if first.Ran != 1 || first.Uncached != 0 {
		t.Fatalf("first run: ran=%d uncached=%d; the record must publish before the digest can stale it", first.Ran, first.Uncached)
	}
	if err := os.WriteFile(filepath.Join(tmp, "data.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := RunTestsFresh(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("second run degraded: %s", second.Degraded)
	}
	if second.Ran != 1 || second.Fresh != 0 {
		t.Fatalf("after input change: ran=%d fresh=%d; the digest must stale the record", second.Ran, second.Fresh)
	}
}
