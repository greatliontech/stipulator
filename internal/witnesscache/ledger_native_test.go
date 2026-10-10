package witnesscache

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestLedgerPersistsCompiledEmbeddedMember(t *testing.T) {
	if testing.Short() {
		t.Skip("captures a native compiled-and-embedded ledger")
	}
	stipulate.Covers(t, "REQ-evidence-witness-cache-format-ledger")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for file, contents := range map[string]string{
		"go.mod":       "module example.com/dual\n\ngo 1.26\n",
		"p.go":         "package dual\n",
		"dual_test.go": "package dual\nimport (\"testing\"; _ \"embed\")\n//go:embed dual_test.go\nvar text string\nfunc TestDual(t *testing.T) { if text == \"\" { t.Fatal(\"missing embedded source\") } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := gofresh.New(gofresh.WithDir(dir), gofresh.WithEnv(gotool.SetEnv(os.Environ(), "GOWORK", "off")...))
	if err != nil {
		t.Fatal(err)
	}
	s := gofresh.Subject{Package: "example.com/dual", Symbol: "TestDual"}
	view, err := engine.NewView(context.Background(), []gofresh.Subject{s}, dir)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := view.Capture(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := view.TestVariantLedger(s)
	if err != nil {
		t.Fatal(err)
	}
	dual := false
	for _, header := range ledger.FileHeaders {
		if header.File == "dual_test.go" {
			dual = header.Embedded && header.Bindings != nil
		}
	}
	if !dual {
		t.Fatal("fixture did not produce compiled+embedded evidence", ledger)
	}
	rec := Record{Group: "group", Package: s.Package, Test: s.Symbol, Fingerprint: fp, CompartmentLedger: LedgerFromGofresh(ledger), Outcomes: map[string]string{s.Package + "." + s.Symbol: "passed"}}
	if err := Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	back := LoadLedger(dir, rec)
	if back == nil || !reflect.DeepEqual(ledger, back.ToGofresh()) {
		t.Fatal("compiled+embedded ledger did not survive install/load", back)
	}
}
