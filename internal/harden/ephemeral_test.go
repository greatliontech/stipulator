package harden

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestRapidBinFlags pins the tree-never-touched guard's decision: a package
// importing rapid gets -rapid.nofailfile so a property failure never writes a
// reproducer into the tree, while a plain package gets no flag (an
// unrecognized flag on a plain binary would read as a false kill).
func TestRapidBinFlags(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-ephemeral")
	b, err := golang.New(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rapidBinFlags(b, "example.com/fixture/lib"); len(got) != 1 || got[0] != "-rapid.nofailfile" {
		t.Errorf("rapid package: got %v, want [-rapid.nofailfile]", got)
	}
	if got := rapidBinFlags(b, "example.com/fixture/plain"); len(got) != 0 {
		t.Errorf("plain package: got %v, want no flags", got)
	}
}

// TestEphemeral pins REQ-harden-ephemeral: a mutant the named test catches is
// killed with the failing test attributed; a mutant in a branch the test does
// not exercise survives; the tree is never touched (the source file, and no
// rapid reproducer, is ever written); and the guards refuse a run that would
// fabricate a survivor — a missing file, an identical mutant, a -run matching
// no test, or a non-compiling mutant.
func TestEphemeral(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	stipulate.Covers(t, "REQ-harden-ephemeral")
	dir := fixtureDir
	libPath := dir + "/lib/lib.go"
	work, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	pkg := "example.com/fixture/lib"
	ctx := context.Background()
	timeout := 60 * time.Second

	// Killed: flipping Add's return breaks both branches TestAdd pins.
	killMutant := bytes.Replace(work, []byte("return a + b"), []byte("return a - b"), 1)
	if bytes.Equal(killMutant, work) {
		t.Fatal("failed to synthesize the kill mutant")
	}
	res, err := Ephemeral(ctx, dir, "lib/lib.go", killMutant, pkg, "^TestAdd$", timeout)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer == "" {
		t.Fatalf("kill mutant was not killed with attribution: %+v", res)
	}
	if after, _ := os.ReadFile(libPath); !bytes.Equal(after, work) {
		t.Fatal("ephemeral run mutated the working tree")
	}

	// Survived: TestWeak never exercises the large-x branch.
	surviveMutant := bytes.Replace(work, []byte("return x - 1"), []byte("return x - 2"), 1)
	res, err = Ephemeral(ctx, dir, "lib/lib.go", surviveMutant, pkg, "^TestWeak$", timeout)
	if err != nil {
		t.Fatal(err)
	}
	if res.Killed {
		t.Fatalf("mutant in an unexercised branch should survive: %+v", res)
	}

	// Guards: each of these would otherwise fabricate a survivor or measure
	// nothing, so each is an error.
	if _, err := Ephemeral(ctx, dir, "lib/nope.go", killMutant, pkg, "^TestAdd$", timeout); err == nil {
		t.Fatal("a missing source file should be an error")
	}
	if _, err := Ephemeral(ctx, dir, "lib/lib.go", work, pkg, "^TestAdd$", timeout); err == nil {
		t.Fatal("an identical mutant should be an error")
	}
	if _, err := Ephemeral(ctx, dir, "lib/lib.go", killMutant, pkg, "^TestNoSuch$", timeout); err == nil {
		t.Fatal("a -run matching no test should be an error, not a survivor")
	}
	if _, err := Ephemeral(ctx, dir, "lib/lib.go", []byte("package lib\nthis is not go\n"), pkg, "^TestAdd$", timeout); err == nil {
		t.Fatal("a non-compiling mutant should be an error")
	}
	// A named test that fails on the unmutated tree cannot attribute a
	// mutant — refused, never a false survivor.
	if _, err := Ephemeral(ctx, dir, "lib/lib.go", killMutant, pkg, "^TestWitFail$", timeout); err == nil {
		t.Fatal("a baseline-failing test should be an error")
	}
	// A named test that only skips witnesses nothing — matched no runnable test.
	if _, err := Ephemeral(ctx, dir, "lib/lib.go", killMutant, pkg, "^TestWitSkip$", timeout); err == nil {
		t.Fatal("a skip-only test should be an error")
	}
}
