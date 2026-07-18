package extread

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestReadsSiblingFixture reads another package's committed fixture: the
// read resolves inside the verification tree but outside this package's
// own directory — the one root its observation bracket declares. The path
// is derived from the test's own compiled source location and joined
// clean, so the recorded input is absolute — no ambiguous parent
// traversal, no working-directory or environment read — and the
// observation's unverifiable reason is exactly the coverage gap.
func TestReadsSiblingFixture(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller source location")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "reads", "testdata", "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty fixture")
	}
}
