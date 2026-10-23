package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func allocationPolicy(packages ...string) *stipulatorv1.TestPolicy {
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages(packages)
	cfg.SetPlainWitness(true)
	cfg.SetAssumePure(true)
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("all", cfg)})
	return policy
}

func TestGoCaptureAllocationPrecedesOperationSymbols(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency", "REQ-check-derivation")
	if testing.Short() {
		t.Skip("discovers fixture packages")
	}
	neutralAmbient(t)
	t.Setenv("GOMAXPROCS", strconv.Itoa(runtime.GOMAXPROCS(0)))
	dir := writeModule(t, map[string]string{
		"go.mod":         "module example.com/allocation\n\ngo 1.26\n",
		"a/a_test.go":    "package a\nimport \"testing\"\nfunc TestA(t *testing.T) {}\n",
		"b/b_test.go":    "package b\nimport \"testing\"\nfunc TestB(t *testing.T) {}\n",
		"empty/empty.go": "package empty\n",
	})
	policy := allocationPolicy("./...")
	for _, tc := range []struct {
		name     string
		full     bool
		scope    map[gofresh.Subject]bool
		packages int
	}{
		{name: "full", full: true, packages: 3},
		{name: "witness", packages: 2},
		{name: "scoped", scope: map[gofresh.Subject]bool{{Package: "example.com/allocation/a", Symbol: "TestA"}: true}, packages: 1},
		{name: "empty scope", scope: map[gofresh.Subject]bool{}, packages: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := countSpawns(t)
			pc := mustCapture(t, t.Context(), dir, policy)
			if err := pc.ConfigureExecution(tc.full, tc.scope); err != nil {
				t.Fatal(err)
			}
			syms, err := OperationSymbols(t.Context(), &records.Store{}, pc)
			if err != nil {
				t.Fatal(err)
			}
			if len(syms) != 2 {
				t.Fatalf("scope narrowed discovery: %v", syms)
			}
			paid := counter.snapshot().derivation()
			if paid.env == 0 || paid.list == 0 {
				t.Fatalf("derivation was not observed: %+v", paid)
			}
			again, err := OperationSymbols(t.Context(), &records.Store{}, pc)
			if err != nil || !reflect.DeepEqual(syms, again) {
				t.Fatalf("second symbols=%v err=%v", again, err)
			}
			d, err := pc.discover(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := counter.snapshot().derivation(); got != paid {
				t.Fatalf("discovery repeated: %+v -> %+v", paid, got)
			}
			n := pc.normalized[0]
			bound := max(1, tc.packages)
			if ceiling := max(1, runtime.GOMAXPROCS(0)/2); bound > ceiling {
				bound = ceiling
			}
			want := strconv.Itoa(runtime.GOMAXPROCS(0) / bound)
			width, _ := lookupEnv(n.WitnessEnv, "GOMAXPROCS")
			if n.SpawnBound != bound || width != want {
				t.Fatalf("bound=%d width=%s, want %d/%s", n.SpawnBound, width, bound, want)
			}
			if len(d.groups) != 1 || !reflect.DeepEqual(d.groups[0].witnessEnv, n.WitnessEnv) {
				t.Fatal("group captured a different width")
			}
		})
	}
}

func TestGoScopedAllocationDeliversPackageWidth(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency", "REQ-check-verdict-scoped")
	if testing.Short() {
		t.Skip("executes fixture packages")
	}
	neutralAmbient(t)
	budget := runtime.GOMAXPROCS(0)
	t.Setenv("GOMAXPROCS", strconv.Itoa(budget))
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/scopedwidth\n\ngo 1.26\n",
		"chosen/chosen_test.go": fmt.Sprintf(`package chosen
import ("flag"; "os"; "runtime"; "testing")
func TestWidth(t *testing.T) {
 if got := flag.Lookup("test.count").Value.String(); got != "3" { t.Fatalf("count = %%s", got) }
 if got := os.Getenv("GOMAXPROCS"); got != %q { t.Fatalf("environment width = %%s", got) }
 if got := runtime.GOMAXPROCS(0); got != %d { t.Fatalf("runtime width = %%d", got) }
}
`, strconv.Itoa(budget), budget),
		"outside/outside_test.go": "package outside\nimport \"testing\"\nfunc TestOutside(t *testing.T) { t.Fatal(\"outside scope executed\") }\n",
	})
	policy := allocationPolicy("./...")
	policy.GetInvocations()[0].GetGo().SetCount(3)
	scope := map[gofresh.Subject]bool{{Package: "example.com/scopedwidth/chosen", Symbol: "TestWidth"}: true}
	pc := mustCapture(t, t.Context(), dir, policy)
	if err := pc.ConfigureExecution(false, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := OperationSymbols(t.Context(), &records.Store{}, pc); err != nil {
		t.Fatal(err)
	}
	tr, err := RunWitnessesScoped(t.Context(), pc, scope, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Ran != 1 || tr.Outcomes["example.com/scopedwidth/chosen.TestWidth"] != verify.TestPassed || len(tr.Diagnostics) != 0 || tr.Degraded != "" {
		t.Fatalf("scoped child did not receive the full budget: %+v", tr)
	}
	counter := countSpawns(t)
	tr, err = RunWitnessesScoped(t.Context(), mustCapture(t, t.Context(), dir, policy), map[gofresh.Subject]bool{}, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if counter.snapshot().test != 0 || tr.Ran != 0 {
		t.Fatalf("zero-package scope spawned tests: %+v / %+v", counter.snapshot(), tr)
	}
}

func TestGoScopedAllocationWarmWidthMovement(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency", "REQ-evidence-witness-freshness-record-identity")
	if testing.Short() {
		t.Skip("executes and serves fixture witnesses")
	}
	checkScopedAllocationWidthMovement(t, true)
}

// Purity and the absence of environment reads must not bypass the compilation
// guard. A second identical run proves the cold result really published before
// a new execution scope changes the delivered width.
func TestGoScopedAllocationWarmUnobservedWidthMovement(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-concurrency", "REQ-evidence-witness-freshness-record-identity")
	if testing.Short() {
		t.Skip("executes and serves ordinary non-environment-reading witnesses")
	}
	checkScopedAllocationWidthMovement(t, false)
}

func checkScopedAllocationWidthMovement(t *testing.T, observesWidth bool) {
	t.Helper()
	if runtime.GOMAXPROCS(0) < 4 {
		t.Skip("two package slots require at least four processors")
	}
	neutralAmbient(t)
	t.Setenv("GOMAXPROCS", strconv.Itoa(runtime.GOMAXPROCS(0)))
	imports, body := `import "testing"`, `if 2+2 != 4 { t.Fatal("arithmetic") }`
	if observesWidth {
		imports, body = `import ("os";"testing")`, `if os.Getenv("GOMAXPROCS") == "" { t.Fatal("missing width") }`
	}
	dir := writeModule(t, map[string]string{
		"go.mod":      "module example.com/widthmove\n\ngo 1.26\n",
		"a/a_test.go": "package a\n" + imports + "\nfunc TestA(t *testing.T) { " + body + " }\n",
		"b/b_test.go": "package b\n" + imports + "\nfunc TestB(t *testing.T) { " + body + " }\n",
	})
	policy := allocationPolicy("./...")
	one := map[gofresh.Subject]bool{{Package: "example.com/widthmove/a", Symbol: "TestA"}: true}
	var identity string
	run := func(scope map[gofresh.Subject]bool, ran, fresh int) {
		t.Helper()
		pc := mustCapture(t, t.Context(), dir, policy)
		tr, err := RunWitnessesScoped(t.Context(), pc, scope, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" || tr.Ran != ran || tr.Fresh != fresh || len(tr.Diagnostics) != 0 {
			t.Fatalf("ran/fresh=%d/%d want %d/%d: %+v", tr.Ran, tr.Fresh, ran, fresh, tr)
		}
		if scope != nil && tr.ExecutedReasons["example.com/widthmove/b.TestB"] != "" {
			t.Fatal("scope-skipped witness reported re-executed")
		}
		got := groupIdentity(pc.normalized[0])
		if identity != "" && identity != got {
			t.Fatal("scope-derived width re-addressed records")
		}
		identity = got
	}
	run(one, 1, 0)
	run(one, 0, 1)
	run(nil, 2, 0)
	run(nil, 0, 2)
	run(one, 0, 1)
}

func TestGoScopedDriftRetriesStayWithinScope(t *testing.T) {
	stipulate.Covers(t, "REQ-check-verdict-scoped", "REQ-evidence-witness-freshness-concurrency", "REQ-evidence-witness-freshness-revalidation")
	if testing.Short() {
		t.Skip("executes a scoped drift and its in-scope retry")
	}
	neutralAmbient(t)
	budget := runtime.GOMAXPROCS(0)
	t.Setenv("GOMAXPROCS", strconv.Itoa(budget))
	width := budget
	if budget >= 4 {
		width = budget / 2
	}
	dir := writeModule(t, map[string]string{
		"go.mod":        "module example.com/scopeddrift\n\ngo 1.26\n",
		"reader/in.txt": "v1", "reader/out.txt": "v1", "writer/trigger.txt": "no",
		"reader/reader_test.go": fmt.Sprintf(`package reader
import ("os"; "runtime"; "testing")
func TestIn(t *testing.T) {
 if _,err := os.ReadFile("in.txt"); err != nil { t.Fatal(err) }
 if got := os.Getenv("GOMAXPROCS"); got != %q { t.Fatalf("retry environment width = %%s",got) }
 if got := runtime.GOMAXPROCS(0); got != %d { t.Fatalf("retry runtime width = %%d",got) }
}
func TestOut(t *testing.T) {
 data,err := os.ReadFile("out.txt"); if err != nil { t.Fatal(err) }
 if string(data) != "v1" { t.Fatal("out-of-scope drift executed") }
}
`, strconv.Itoa(width), width),
		"writer/writer_test.go": `package writer
import ("os";"testing")
func TestWrite(t *testing.T) {
 data,err := os.ReadFile("trigger.txt"); if err != nil { t.Fatal(err) }
 if string(data) != "yes" { return }
 for _,path := range []string{"../reader/in.txt","../reader/out.txt"} {
  if err := os.WriteFile(path,[]byte("v2"),0600); err != nil { t.Fatal(err) }
 }
}
`,
	})
	policy := allocationPolicy("./...")
	run := func(scope map[gofresh.Subject]bool) *verify.TestRun {
		t.Helper()
		tr, err := RunWitnessesScoped(t.Context(), mustCapture(t, t.Context(), dir, policy), scope, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" || len(tr.Diagnostics) != 0 {
			t.Fatalf("witness run failed: %+v", tr)
		}
		return tr
	}
	if cold := run(nil); cold.Ran != 3 || cold.Fresh != 0 {
		t.Fatalf("cold run: %+v", cold)
	}
	if warm := run(nil); warm.Ran != 0 || warm.Fresh != 3 {
		t.Fatalf("fixture did not establish three valid serves: %+v", warm)
	}
	if err := os.WriteFile(filepath.Join(dir, "writer", "trigger.txt"), []byte("yes"), 0600); err != nil {
		t.Fatal(err)
	}
	scope := map[gofresh.Subject]bool{
		{Package: "example.com/scopeddrift/reader", Symbol: "TestIn"}:    true,
		{Package: "example.com/scopeddrift/writer", Symbol: "TestWrite"}: true,
	}
	var retried []string
	prior := afterRetryWaveForTest
	afterRetryWaveForTest = func(merge *execMerge) {
		for _, row := range merge.rows {
			if row.GetTest() == topLevel(row.GetTest()) {
				retried = append(retried, row.GetPackage()+"."+row.GetTest())
			}
		}
	}
	t.Cleanup(func() { afterRetryWaveForTest = prior })
	tr := run(scope)
	if tr.Ran != 2 || tr.Fresh != 0 || tr.Outcomes["example.com/scopeddrift/reader.TestIn"] != verify.TestPassed || tr.Outcomes["example.com/scopeddrift/writer.TestWrite"] != verify.TestPassed {
		t.Fatalf("in-scope drift did not retry correctly: %+v", tr)
	}
	out := "example.com/scopeddrift/reader.TestOut"
	if _, ok := tr.Outcomes[out]; ok || !tr.ScopeSkipped[out] || tr.NoOutcome[out] != "" || tr.ExecutedReasons[out] != "" {
		t.Fatalf("invalid out-of-scope serve not withheld as scope-skipped: %+v", tr)
	}
	if !reflect.DeepEqual(retried, []string{"example.com/scopeddrift/reader.TestIn"}) {
		t.Fatalf("retry selection=%v", retried)
	}
}
