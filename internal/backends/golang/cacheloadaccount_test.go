package golang

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestNativeCacheRefusalsRemainAdvisoryAcrossExecutionAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("executes native witnesses around historical and malformed records")
	}
	if dir := os.Getenv("STIPULATOR_NATIVE_REFUSAL_RESTART"); dir != "" {
		loaded := witnesscache.Load(t.Context(), dir)
		if len(loaded.Records) != 0 || len(loaded.Account.GetRefusals()) != 1 || loaded.Account.GetRefusals()[0].GetCoordinate() == nil {
			t.Fatalf("restart upgraded refused evidence or lost its account: %+v", loaded)
		}
		return
	}
	stipulate.Covers(t, "REQ-evidence-outcome-premises", "REQ-evidence-witness-cache-format-validation", "REQ-evidence-flip-environment")
	neutralAmbient(t)
	t.Setenv("STIPULATOR_NATIVE_REFUSAL_FAIL", "")
	for _, name := range []string{"historical-v2", "historical-v3", "malformed"} {
		t.Run(name, func(t *testing.T) {
			dir := writeModule(t, map[string]string{
				"go.mod":    "module example.com/refused\n\ngo 1.26\n",
				"p_test.go": "package p\nimport (\"os\"; \"testing\")\nfunc TestEnv(t *testing.T) { if os.Getenv(\"PWD\") == \"\" { t.Fatal(\"missing PWD\") }; if os.Getenv(\"STIPULATOR_NATIVE_REFUSAL_FAIL\") == \"1\" { t.Fatal(\"actual failing execution\") } }\n",
			})
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{"."})
			cfg.SetPlainWitness(true)
			policy := &stipulatorv1.TestPolicy{}
			policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("native", cfg)})
			writePolicyRecord(t, dir, policy)
			const key = "example.com/refused.TestEnv"
			cold, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || cold.Outcomes[key] != verify.TestPassed || cold.Uncached != 0 {
				t.Fatalf("native producer: %+v %v", cold, err)
			}
			loaded := witnesscache.Load(t.Context(), dir)
			if len(loaded.Records) != 1 || loaded.Account != nil {
				t.Fatalf("native record: %+v", loaded)
			}
			current := loaded.Records[0]
			historical := current
			manifest, err := base64.RawURLEncoding.DecodeString(current.Fingerprint.RuntimeInputs)
			if err != nil || !bytes.Contains(manifest, []byte(`"v":4`)) {
				t.Fatalf("producer did not supply v4: %s %v", manifest, err)
			}
			if name == "malformed" {
				manifest = []byte(`{"v":4`)
			} else {
				version := []byte(`"v":2`)
				if name == "historical-v3" {
					version = []byte(`"v":3`)
				}
				manifest = bytes.Replace(manifest, []byte(`"v":4`), version, 1)
			}
			historical.Fingerprint.RuntimeInputs = base64.RawURLEncoding.EncodeToString(manifest)
			store, err := witnesscache.StoreDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			currentName, err := recordstore.Name([]string{current.Group, current.Package, current.Test}, current.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(store, currentName)); err != nil {
				t.Fatal(err)
			}
			if err := witnesscache.Install(t.Context(), dir, historical); err != nil {
				t.Fatal(err)
			}
			historicalName, err := recordstore.Name([]string{historical.Group, historical.Package, historical.Test}, historical.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			historicalPath := filepath.Join(store, historicalName)
			before, err := os.ReadFile(historicalPath)
			if err != nil {
				t.Fatal(err)
			}
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNativeCacheRefusalsRemainAdvisoryAcrossExecutionAndRestart$", "-test.count=1")
			child.Env = gotool.SetEnv(os.Environ(), "STIPULATOR_NATIVE_REFUSAL_RESTART", dir)
			if out, err := child.CombinedOutput(); err != nil {
				t.Fatalf("restart: %s %v", out, err)
			}
			checkAccount := func(run *verify.TestRun) {
				t.Helper()
				rows := run.WitnessCacheLoad.GetRefusals()
				if len(rows) != 1 || rows[0].GetCause() != stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_RUNTIME_MANIFEST {
					t.Fatalf("load account: %v", run.WitnessCacheLoad)
				}
				coordinate := rows[0].GetCoordinate()
				if coordinate == nil || coordinate.GetGroup() != current.Group || coordinate.GetPackage() != current.Package || coordinate.GetTest() != current.Test {
					t.Fatalf("refusal attribution: %v", rows[0])
				}
			}
			policyRefused, err := RunWitnesses(t.Context(), dir, stubSeeding{key: true})
			if err != nil {
				t.Fatal(err)
			}
			checkAccount(policyRefused)
			if class, known := classifyReason(policyRefused.ExecutedReasons[key]); !known || class != reasonSeeded {
				t.Fatalf("load fallback hid the current serving policy: %v", policyRefused.ExecutedReasons)
			}
			if len(witnesscache.Load(t.Context(), dir).Records) != 0 {
				t.Fatal("policy-refused execution published reusable evidence")
			}
			// A refused historical pass is not prior passing evidence for a
			// failure flip. Only admitted Records may feed that enrichment.
			t.Setenv("STIPULATOR_NATIVE_REFUSAL_FAIL", "1")
			failed, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || failed.Outcomes[key] != verify.TestFailed || strings.Contains(failed.Failures[key], "runner execution environment") {
				t.Fatalf("refused outcome authorized flip diagnostics: %+v %v", failed, err)
			}
			checkAccount(failed)
			class, known := classifyReason(failed.ExecutedReasons[key])
			if !known || class != reasonStoreRefused {
				t.Fatalf("missing attributed refusal reason: %v", failed.ExecutedReasons)
			}
			t.Setenv("STIPULATOR_NATIVE_REFUSAL_FAIL", "")
			rerun, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || rerun.Ran != 1 || rerun.Fresh != 0 || rerun.Outcomes[key] != verify.TestPassed {
				t.Fatalf("refusal did not re-execute: %+v %v", rerun, err)
			}
			checkAccount(rerun)
			warm, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || warm.Ran != 0 || warm.Fresh != 1 || warm.ExecutedReasons[key] != "" {
				t.Fatalf("valid sibling did not supersede refusal fallback: %+v %v", warm, err)
			}
			checkAccount(warm)
			// The admitted v4 sibling is now genuine prior evidence. Its native
			// staleness outranks the load fallback, and its pass may enrich a flip.
			t.Setenv("STIPULATOR_NATIVE_REFUSAL_FAIL", "1")
			flipped, err := RunWitnesses(t.Context(), dir, noSeeding{})
			if err != nil || flipped.Outcomes[key] != verify.TestFailed || !strings.Contains(flipped.Failures[key], "runner execution environment") {
				t.Fatalf("admitted sibling did not authorize the genuine flip account: %+v %v", flipped, err)
			}
			checkAccount(flipped)
			if class, known := classifyReason(flipped.ExecutedReasons[key]); flipped.ExecutedReasons[key] == "" || known && class == reasonStoreRefused {
				t.Fatalf("load fallback hid the native verdict: %v", flipped.ExecutedReasons)
			}
			t.Setenv("STIPULATOR_NATIVE_REFUSAL_FAIL", "")
			after, err := os.ReadFile(historicalPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("inspection or re-execution rewrote historical support: %v", err)
			}
		})
	}
}
