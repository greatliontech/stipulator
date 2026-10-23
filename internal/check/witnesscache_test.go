package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestWitnessCacheCheckCompositionHasOneRootHome(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-cache-refusals", "REQ-mcp-response-contract")
	neutralAmbient(t)
	dir := writeTree(t, baseTree(map[string]string{
		".stipulator/policy.textproto":       "invocations { name: \"plain\" timeout { seconds: 300 } go { packages: \"./...\" plain_witness: true } }\n",
		".stipulator/bindings/fix.textproto": "bindings { requirement_id: \"REQ-fix-may\" backend: \"go\" symbol: \"example.com/checkfix/ok.TestDouble\" role: BINDING_ROLE_TESTS }\n",
	}))
	cache, err := witnesscache.StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "refused.json"), []byte("not an envelope"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, scoped := range []bool{false, true} {
		var ids []string
		if scoped {
			ids = []string{"REQ-fix-may"}
		}
		res, err := Run(t.Context(), dir, false, ids)
		if err != nil {
			t.Fatal(err)
		}
		a := res.GetWitnessCacheLoad()
		if len(a.GetRefusals()) != 1 || a.GetOmitted() != 0 || a.GetIncomplete() || a.GetRefusals()[0].GetFile() != "refused.json" {
			t.Fatalf("scoped=%v: check lost actual load refusal: %v", scoped, a)
		}
		if res.GetVerify() == nil || res.GetCoverage() == nil || res.GetVerify().GetWitnessCacheLoad() != nil || res.GetCoverage().GetWitnessCacheLoad() != nil {
			t.Fatalf("scoped=%v: account duplicated or report missing: %v", scoped, res)
		}
		if res.GetSuiteHealthJudged() || len(res.GetWitnessDiagnostics()) != 0 || res.GetTestsExecuted()+res.GetTestsServed() != 1 {
			t.Fatalf("advisory refusal changed witness or health accounting: %v", res)
		}
	}
	// Whole-policy health execution does not consult the witness store.
	full, err := Run(t.Context(), dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !full.GetSuiteHealthJudged() || full.GetWitnessCacheLoad() != nil {
		t.Fatalf("health form invented a load: %v", full)
	}
}
