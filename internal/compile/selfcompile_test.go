package compile

import (
	"os"
	"testing"
)

// TestSelfCompile compiles this repository's own spec corpus: the corpus
// must always be profile-clean, and the compiler's first real input is its
// own contract.
//
//gofresh:pure
func TestSelfCompile(t *testing.T) {
	spec, diags, err := Compile(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		t.Error(d)
	}
	if t.Failed() {
		t.Fatalf("own corpus does not compile: %d diagnostics", len(diags))
	}
	if n := len(spec.GetRequirements()); n < 40 {
		t.Fatalf("suspiciously few requirements: %d", n)
	}
	if n := len(spec.GetTerms()); n < 15 {
		t.Fatalf("suspiciously few terms: %d", n)
	}
	if n := len(spec.GetEdges()); n == 0 {
		t.Fatal("no edges")
	}
}
