package golang

import (
	"os"
	"path/filepath"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func independentModulePolicy(t *testing.T) string {
	t.Helper()
	dir := writeModule(t, map[string]string{
		"go.mod":             "module example.com/outer\n\ngo 1.27\n",
		"p.go":               "package outer\nfunc F() int { return 1 }\n",
		"p_test.go":          "package outer\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"outer\") } }\n",
		"consumer/go.mod":    "module example.net/independent\n\ngo 1.27\n",
		"consumer/p.go":      "package independent\nfunc F() int { return 2 }\n",
		"consumer/p_test.go": "package independent\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=2 { t.Fatal(\"consumer\") } }\n",
	})
	p := &stipulatorv1.TestPolicy{}
	var invs []*stipulatorv1.PolicyInvocation
	for _, root := range []string{"", "consumer"} {
		cfg := &stipulatorv1.GoInvocationConfig{}
		cfg.SetPackages([]string{"./..."})
		cfg.SetModuleRoot(root)
		cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
		cfg.SetPlainWitness(true)
		invs = append(invs, goInvocation("module-"+root, cfg))
	}
	p.SetInvocations(invs)
	writePolicyRecord(t, dir, p)
	return dir
}

// Independent nested modules are neither workspace members nor root-module
// dependencies. Their accepted roots remain the coordinates of both forms.
func TestIndependentModuleRootsReachResolutionAndWitnesses(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a two-module plain-witness policy")
	}
	stipulate.Covers(t, "REQ-go-build-selections", "REQ-go-workspace", "REQ-evidence-witness-freshness-inputs")
	for _, mode := range []string{"resolution", "full", "selective"} {
		t.Run(mode, func(t *testing.T) {
			neutralAmbient(t)
			dir := independentModulePolicy(t)
			symbols := []string{"example.com/outer.TestF", "example.net/independent.TestF"}
			if mode == "resolution" {
				b, err := NewServed(t.Context(), dir, symbols)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
				for _, symbol := range symbols {
					if res, _, err := b.Resolve(symbol); err != nil || res != verify.Resolved {
						t.Errorf("%s: %v %v", symbol, res, err)
					}
				}
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
				again, err := NewServed(t.Context(), dir, symbols)
				if err != nil {
					t.Fatal(err)
				}
				defer again.Close()
				if again.ServedCount() != 2 {
					t.Fatalf("resolution records: served %d; %v", again.ServedCount(), b.Notices())
				}
				return
			}
			pc, err := LoadCapture(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			var run *verify.TestRun
			seeding, err := NewWholeTree(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer seeding.Close()
			if mode == "full" {
				report, got, err := ExecutePolicyWitnessed(t.Context(), pc, seeding)
				if err != nil || !SuiteHealthy(report) {
					t.Fatalf("full execution: %v\n%v", err, report)
				}
				run = got
			} else {
				run, err = RunWitnessesPolicy(t.Context(), pc, seeding)
				if err != nil {
					t.Fatal(err)
				}
			}
			if run.Degraded != "" || run.Uncached != 0 {
				t.Fatalf("freshness lost the module root: %+v", run)
			}
			for _, symbol := range symbols {
				if run.Outcomes[symbol] != verify.TestPassed {
					t.Errorf("missing %s outcome: %v", symbol, run.Outcomes)
				}
			}
			if got := witnesscache.Load(t.Context(), dir); len(got) != 2 {
				t.Fatalf("published %d records, want both independent modules", len(got))
			}
			freshRun := func() (*verify.TestRun, error) {
				backend, err := NewWholeTree(t.Context(), dir)
				if err != nil {
					return nil, err
				}
				defer backend.Close()
				return RunWitnesses(t.Context(), dir, backend)
			}
			again, err := freshRun()
			if err != nil || again.Fresh != 2 || again.Ran != 0 {
				t.Fatalf("warm independent modules: %+v, %v", again, err)
			}
			for _, member := range []string{"consumer", "."} {
				path := filepath.Join(dir, member, "p.go")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, []byte("\nfunc Added() {}\n")...), 0o644); err != nil {
					t.Fatal(err)
				}
				moved, err := freshRun()
				if err != nil || moved.Degraded != "" || moved.Uncached != 0 || moved.Ran != 1 || moved.Fresh != 1 {
					t.Fatalf("edit in %q did not preserve the independent module: %+v, %v", member, moved, err)
				}
			}
		})
	}
}
