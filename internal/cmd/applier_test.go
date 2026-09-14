package cmd

import (
	"path/filepath"
	"testing"

	"github.com/greatliontech/stipulator/internal/recordapply"

	"github.com/greatliontech/stipulator/stipulate"
)

// One applier per root: the one-apply-at-a-time rule is the applier's,
// so every verb writing a root shares its applier (REQ-record-cas).
//
//gofresh:pure
func TestApplierIsOnePerRoot(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	a, b := t.TempDir(), t.TempDir()
	at := func(dir string) *recordapply.Applier {
		t.Helper()
		ap, err := applierAt(dir)
		if err != nil {
			t.Fatal(err)
		}
		return ap
	}
	if at(a) != at(a) {
		t.Fatal("two applies to one root got two appliers")
	}
	if at(a) == at(b) {
		t.Fatal("two roots share an applier")
	}
	// Two spellings of one root share its applier.
	if at(a+string(filepath.Separator)) != at(a) || at(filepath.Join(a, "x", "..")) != at(a) {
		t.Fatal("a root's spellings got their own appliers")
	}
	t.Chdir(a)
	if at(".") != at(a) {
		t.Fatal("the relative spelling of the working directory got its own applier")
	}
}
