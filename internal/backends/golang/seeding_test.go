package golang

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"

	"errors"
	"github.com/greatliontech/gofresh"
	"maps"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/policy"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// noSeeding is the seeding classifier for fixtures holding no
// random-seeded witness: nothing is seeded, so every subject serves and
// publishes exactly as the freshness contract's ordinary case.
type noSeeding struct{}

func (noSeeding) NeverServe([]string) (map[string]string, error) { return map[string]string{}, nil }

// stubSeeding refuses exactly the named symbols with the seeded reason:
// the seam for pinning what the serving rounds do with a proven-fresh
// record the contract refuses, over a fixture whose deterministic
// witness publishes by closure.
type stubSeeding map[string]bool

func (s stubSeeding) NeverServe(symbols []string) (map[string]string, error) {
	out := map[string]string{}
	for _, sym := range symbols {
		if s[sym] {
			out[sym] = seededReason
		}
	}
	return out, nil
}

// faultingSeeding is a classifier whose answer is a fault: the run must
// fail closed on it — serving degrades to execution and nothing
// publishes.
type faultingSeeding struct{}

func (faultingSeeding) NeverServe([]string) (map[string]string, error) {
	return nil, errors.New("resolver child died")
}

// rapidModule is a fixture module holding one random-seeded witness (a
// rapid-driven property test) beside one deterministic example
// witness, in separate packages: a package reaching the harness is
// unverifiable-by-hash at the closure tier and publishes only through
// the observation path, so the deterministic sibling lives where its
// closure alone proves it. The dependency is the real audited harness
// release — the freshness engine admits observation through exactly
// that version — so the serving decision under test is stipulator's
// own, never a closure refusal of a stand-in.
func rapidModule(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod":          "module example.com/seeded\n\ngo 1.26\n\nrequire pgregory.net/rapid v1.3.0\n",
		"go.sum":          "pgregory.net/rapid v1.3.0 h1:vBvO0VSqti75J1jjYqpgPNBLKMd1+gxa9fYo7vk/Exc=\npgregory.net/rapid v1.3.0/go.mod h1:dPlE4OBBxgXPqkP79flB6sJL1dx5azpI7HQ9MY9Z7uk=\n",
		"lib/lib.go":      "package lib\n\nfunc Add(a, b int) int { return a + b }\n",
		"lib/lib_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestExample(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
		"prop/prop_test.go": "package prop\n\nimport (\n\t\"testing\"\n\n\t\"example.com/seeded/lib\"\n\t\"pgregory.net/rapid\"\n)\n\n" +
			"func TestProperty(t *testing.T) {\n\trapid.Check(t, func(rt *rapid.T) {\n\t\tif lib.Add(2, 2) != 4 {\n\t\t\tpanic(\"broken\")\n\t\t}\n\t})\n}\n",
	})
}

// TestRandomSeeded pins the seeded form of the property classification:
// a driver-quantified body (rapid.Check / rapid.MakeCheck, gopter's
// TestingRun) is random-seeded, a fuzz target replaying committed seeds
// is not, an example witness is not, and a symbol the loaded views
// cannot inspect is refused under its own reason naming the gap —
// absence of proof never serves, but a load gap never reads as a
// property witness (REQ-go-witness-class,
// REQ-evidence-witness-freshness).
func TestRandomSeeded(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the repository-tree backend the full tier loads before the testlog")
	}
	stipulate.Covers(t, "REQ-go-witness-class", "REQ-evidence-witness-freshness")
	fb := fixtureBackend(t)
	refused, err := fb.NeverServe([]string{
		"example.com/fixture/lib.TestPropRapidCheck",
		"example.com/fixture/lib.TestPropRapidMakeCheck",
		"example.com/fixture/lib.TestGopterProp",
		"example.com/fixture/lib.TestPropRapidGeneratorOnly",
		"example.com/fixture/lib.TestGopterRegistrationOnly",
		"example.com/fixture/lib.TestPropDotImported",
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.NoSuchTest",
	})
	if err != nil {
		t.Fatal(err)
	}
	seededOnly := map[string]bool{}
	for sym, why := range refused {
		seededOnly[sym] = why == seededReason
	}
	want := map[string]bool{
		"example.com/fixture/lib.TestPropRapidCheck":     true,
		"example.com/fixture/lib.TestPropRapidMakeCheck": true,
		"example.com/fixture/lib.TestGopterProp":         true,
		"example.com/fixture/lib.NoSuchTest":             false,
	}
	if !maps.Equal(seededOnly, want) {
		t.Fatalf("NeverServe = %v, want seeded %v", refused, want)
	}
	if why := refused["example.com/fixture/lib.NoSuchTest"]; !strings.Contains(why, "unclassifiable witness") || strings.Contains(why, "random-seeded") {
		t.Fatalf("unclassifiable refusal = %q, want the load gap named and no property attribution", why)
	}
	// A fuzz target is property by harness, deterministic by replay.
	fuzz, err := backend.NeverServe([]string{mod + "/internal/canon.FuzzTextProjection"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fuzz) != 0 {
		t.Fatalf("fuzz target refused serving: %v", fuzz)
	}
}

// TestServingCandidatesExcludeRandomSeeded pins the serving partition
// directly: a random-seeded witness never enters the serving rounds
// whether or not the store holds a record for it — a held record is
// the refused evidence, attributed as the subject's re-execution
// reason — while every other subject enters with its group's records
// (REQ-evidence-witness-freshness).
//
//gofresh:pure
func TestServingCandidatesExcludeRandomSeeded(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	example := gofresh.Subject{Package: "example.com/m", Symbol: "TestExample"}
	recorded := gofresh.Subject{Package: "example.com/m", Symbol: "TestPropertyRecorded"}
	unrecorded := gofresh.Subject{Package: "example.com/m", Symbol: "TestPropertyFresh"}
	cached := map[string][]witnesscache.Record{
		"g\x00example.com/m.TestExample":          {{Package: "example.com/m", Test: "TestExample"}},
		"g\x00example.com/m.TestPropertyRecorded": {{Package: "example.com/m", Test: "TestPropertyRecorded"}},
	}
	executedWhy := map[gofresh.Subject]string{}
	serving, groupCached := servingCandidates("g", []gofresh.Subject{example, recorded, unrecorded},
		map[gofresh.Subject]string{recorded: seededReason, unrecorded: seededReason}, cached, executedWhy)
	if len(serving) != 1 || serving[0] != example {
		t.Fatalf("serving candidates = %v, want the deterministic witness alone", serving)
	}
	if len(groupCached["example.com/m.TestExample"]) != 1 || len(groupCached) != 1 {
		t.Fatalf("group records = %v, want the deterministic witness's record alone", groupCached)
	}
	if executedWhy[recorded] != seededReason {
		t.Fatalf("re-execution reason for the recorded seeded witness = %q, want %q", executedWhy[recorded], seededReason)
	}
	if why, ok := executedWhy[unrecorded]; ok {
		t.Fatalf("a seeded witness with no record carries a re-execution reason: %q", why)
	}
}

// TestClassifySeededAttributesEveryRefusal pins that a refusal always
// carries a reason: a classifier answering with an empty string still
// refuses, and the attribution names that rather than reading empty
// (REQ-evidence-witness-freshness's diagnosable-set requirement).
//
//gofresh:pure
func TestClassifySeededAttributesEveryRefusal(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	s := gofresh.Subject{Package: "example.com/m", Symbol: "TestX"}
	g := &captureGroup{tests: map[string][]string{"example.com/m": {"TestX"}}}
	pc := &policyDiscovery{groups: []*captureGroup{g}}
	if err := classifySeeded(pc, emptyReasonSeeding{}); err != nil {
		t.Fatal(err)
	}
	if why := g.neverServes[s]; why == "" || !strings.Contains(why, "without a stated reason") {
		t.Fatalf("empty classifier reason attributed as %q", why)
	}
}

// emptyReasonSeeding refuses every symbol with no reason at all.
type emptyReasonSeeding struct{}

func (emptyReasonSeeding) NeverServe(symbols []string) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range symbols {
		out[s] = ""
	}
	return out, nil
}

// TestGoRunWitnessesRandomSeededNeverServes pins the serving contract
// end to end: a random-seeded witness executes on every run — a warm
// store serves its deterministic sibling and re-executes it — it
// publishes no record, and its refusal is attributed as uncacheable
// with the seeded reason (REQ-evidence-witness-freshness).
func TestGoRunWitnessesRandomSeededNeverServes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := rapidModule(t)
	writeRacePolicy(t, tmp)
	seeding, err := newContext(context.Background(), tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	const example, property = "example.com/seeded/lib.TestExample", "example.com/seeded/prop.TestProperty"

	first, err := RunWitnesses(context.Background(), tmp, seeding)
	if err != nil {
		t.Fatal(err)
	}
	if first.Degraded != "" {
		t.Fatalf("first run degraded: %s", first.Degraded)
	}
	if first.Ran != 2 || first.Fresh != 0 {
		t.Fatalf("first run ran=%d fresh=%d, want 2 executed, 0 served", first.Ran, first.Fresh)
	}
	if first.Outcomes[property] != verify.TestPassed || first.Outcomes[example] != verify.TestPassed {
		t.Fatalf("first run outcomes = %v", first.Outcomes)
	}
	if got := first.UncacheableReasons[property]; got != seededReason {
		t.Fatalf("seeded witness uncacheable reason = %q, want %q", got, seededReason)
	}
	if _, refused := first.UncacheableReasons[example]; refused {
		t.Fatalf("deterministic sibling refused publication: %q", first.UncacheableReasons[example])
	}
	for _, rec := range witnesscache.Load(tmp) {
		if rec.Test == "TestProperty" {
			t.Fatalf("random-seeded witness published a record: %+v", rec)
		}
	}

	second, err := RunWitnesses(context.Background(), tmp, seeding)
	if err != nil {
		t.Fatal(err)
	}
	if second.Degraded != "" {
		t.Fatalf("second run degraded: %s", second.Degraded)
	}
	if second.Fresh != 1 || second.Ran != 1 {
		t.Fatalf("second run fresh=%d ran=%d, want the sibling served and the seeded witness executed", second.Fresh, second.Ran)
	}
	if second.Outcomes[property] != verify.TestPassed {
		t.Fatalf("seeded witness outcome on re-execution = %v", second.Outcomes[property])
	}
	if got := second.UncacheableReasons[property]; got != seededReason {
		t.Fatalf("seeded witness uncacheable reason on re-execution = %q", got)
	}
	if second.Uncached != 1 {
		t.Fatalf("uncacheable count = %d, want exactly the seeded witness", second.Uncached)
	}

	// The store now holds a proven-fresh record for the deterministic
	// witness. A classifier refusing that witness must leave the record
	// unserved — the exact shape of a store published before the
	// contract held — attributing both the re-execution and the refused
	// publication, and publishing nothing new.
	before := len(witnesscache.Load(tmp))
	refused, err := RunWitnesses(context.Background(), tmp, stubSeeding{example: true, property: true})
	if err != nil {
		t.Fatal(err)
	}
	if refused.Degraded != "" || refused.Fresh != 0 || refused.Ran != 2 {
		t.Fatalf("refusing run degraded=%q fresh=%d ran=%d, want the held record refused and both executed", refused.Degraded, refused.Fresh, refused.Ran)
	}
	if got := refused.ExecutedReasons[example]; got != seededReason {
		t.Fatalf("re-execution reason over the refused record = %q, want %q", got, seededReason)
	}
	if got := refused.UncacheableReasons[example]; got != seededReason {
		t.Fatalf("refused witness uncacheable reason = %q, want %q", got, seededReason)
	}
	if after := len(witnesscache.Load(tmp)); after != before {
		t.Fatalf("store grew from %d to %d records under a refusing classifier", before, after)
	}
}

// TestGoRunWitnessesSeedingFaultFailsClosed pins the fault direction: a
// classifier that cannot answer degrades serving whole — a warm store
// serves nothing, everything executes, nothing publishes — and the
// degraded reason names the classification
// (REQ-evidence-witness-freshness, REQ-evidence-freshness-degrade).
func TestGoRunWitnessesSeedingFaultFailsClosed(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented selective run over a temporary module")
	}
	neutralAmbient(t)
	tmp := rapidModule(t)
	writeRacePolicy(t, tmp)
	warm, err := RunWitnesses(context.Background(), tmp, noSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	published := map[string]bool{}
	for _, rec := range witnesscache.Load(tmp) {
		published[rec.Test] = true
	}
	if warm.Ran != 2 || !published["TestExample"] {
		t.Fatalf("warm-up ran=%d published=%v, want the deterministic witness published", warm.Ran, published)
	}

	faulted, err := RunWitnesses(context.Background(), tmp, faultingSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(faulted.Degraded, "classifying random-seeded witnesses") || !strings.Contains(faulted.Degraded, "resolver child died") {
		t.Fatalf("degraded reason = %q, want the classification fault named", faulted.Degraded)
	}
	if faulted.Fresh != 0 || faulted.Ran != 2 {
		t.Fatalf("faulted run fresh=%d ran=%d, want nothing served and everything executed", faulted.Fresh, faulted.Ran)
	}
	if faulted.Outcomes["example.com/seeded/prop.TestProperty"] != verify.TestPassed {
		t.Fatalf("faulted run withheld executed evidence: %v", faulted.Outcomes)
	}
	if got := len(witnesscache.Load(tmp)); got != 1 {
		t.Fatalf("store holds %d records after the faulted run, want the warm-up's one — a degraded run publishes nothing", got)
	}
}

// TestExecutePolicyWitnessedSeedingFaultFailsClosed pins the same fault
// direction on the health-judged form: the recorder degrades, the
// suite still executes and its evidence stands, and nothing publishes
// (REQ-evidence-witness-freshness, REQ-evidence-freshness-degrade).
func TestExecutePolicyWitnessedSeedingFaultFailsClosed(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-evidence-freshness-degrade")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a temporary module")
	}
	neutralAmbient(t)
	tmp := rapidModule(t)
	writeRacePolicy(t, tmp)
	p, _, err := policy.Load(tmp, map[string]policy.Backend{"go": Policy{}})
	if err != nil {
		t.Fatal(err)
	}
	report, tr, err := ExecutePolicyWitnessed(context.Background(), mustCapture(t, context.Background(), tmp, p), faultingSeeding{})
	if err != nil {
		t.Fatal(err)
	}
	if !SuiteHealthy(report) {
		t.Fatalf("fixture suite unhealthy: %v", report.GetDiagnostics())
	}
	if !strings.Contains(tr.Degraded, "classifying random-seeded witnesses") {
		t.Fatalf("degraded reason = %q, want the classification fault named", tr.Degraded)
	}
	if tr.Outcomes["example.com/seeded/lib.TestExample"] != verify.TestPassed {
		t.Fatalf("degraded full run withheld executed evidence: %v", tr.Outcomes)
	}
	if got := len(witnesscache.Load(tmp)); got != 0 {
		t.Fatalf("degraded full run published %d records, want none", got)
	}
}

// TestExecutePolicyWitnessedRandomSeededNeverPublishes pins the same
// contract on the health-judged form: the whole execution publishes
// records for its deterministic witnesses and none for the random-seeded
// one, whose refusal carries the seeded reason
// (REQ-evidence-witness-freshness).
func TestExecutePolicyWitnessedRandomSeededNeverPublishes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	if testing.Short() {
		t.Skip("executes a race-instrumented policy over a temporary module")
	}
	neutralAmbient(t)
	tmp := rapidModule(t)
	writeRacePolicy(t, tmp)
	seeding, err := newContext(context.Background(), tmp, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := policy.Load(tmp, map[string]policy.Backend{"go": Policy{}})
	if err != nil {
		t.Fatal(err)
	}
	report, tr, err := ExecutePolicyWitnessed(context.Background(), mustCapture(t, context.Background(), tmp, p), seeding)
	if err != nil {
		t.Fatal(err)
	}
	if !SuiteHealthy(report) {
		t.Fatalf("fixture suite unhealthy: %v", report.GetDiagnostics())
	}
	if got := tr.UncacheableReasons["example.com/seeded/prop.TestProperty"]; got != seededReason {
		t.Fatalf("seeded witness uncacheable reason = %q, want %q", got, seededReason)
	}
	records := witnesscache.Load(tmp)
	if len(records) != 1 || records[0].Test != "TestExample" {
		t.Fatalf("records after a full execution = %+v, want the deterministic witness alone", records)
	}
}

// A driver reached only through in-module helpers keeps its example
// evidence class and is refused serving under a reason naming the first
// helper — one hop, two hops, a method helper, and a helper that recurses
// before driving alike — while a helper that reaches no driver serves,
// a driverless cycle among helpers terminating the walk,
// (REQ-evidence-witness-freshness's transitive seeding class beside
// REQ-go-witness-class's direct-call classification).
func TestHelperIndirectedDriverRefusesServing(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the fixture backend the full tier loads")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-witness-class")
	fb := fixtureBackend(t)
	symbols := []string{
		"example.com/fixture/lib.TestPropViaHelper",
		"example.com/fixture/lib.TestPropViaTwoHops",
		"example.com/fixture/lib.TestPropViaMethod",
		"example.com/fixture/lib.TestPropViaCycle",
		"example.com/fixture/lib.TestPlainViaHelper",
		"example.com/fixture/lib.TestPlainViaCycle",
		"example.com/fixture/lib.TestPropViaOtherPackage",
		"example.com/fixture/lib.TestPropViaGenericMethod",
		"example.com/fixture/lib.TestPropViaDependencyHelper",
		"example.com/fixture/lib.TestPropRapidCheck",
		"example.com/fixture/lib.TestProofThenDrive",
		"example.com/fixture/lib.TestDriveThenProof",
		"example.com/fixture/lib.TestProofViaHelper",
	}
	for _, sym := range symbols[:9] {
		if got := fb.WitnessClass(sym); got != verify.ExampleWitness {
			t.Errorf("%s classified %v, want example — the evidence class stays direct-call", sym, got)
		}
	}
	refused, err := fb.NeverServe(symbols)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"example.com/fixture/lib.TestPropViaHelper":  seededThroughReason("example.com/fixture/lib.runProp"),
		"example.com/fixture/lib.TestPropViaTwoHops": seededThroughReason("example.com/fixture/lib.runPropTwice"),
		"example.com/fixture/lib.TestPropViaMethod":  seededThroughReason("(example.com/fixture/lib.propRunner).Run"),
		"example.com/fixture/lib.TestPropViaCycle":   seededThroughReason("example.com/fixture/lib.spin"),
		// The other package's helper, the instantiated generic method
		// (resolved to its origin); the dependency's own helper is
		// outside the walk and serves.
		"example.com/fixture/lib.TestPropViaOtherPackage":  seededThroughReason("example.com/fixture/helpers.Run"),
		"example.com/fixture/lib.TestPropViaGenericMethod": seededThroughReason("(example.com/fixture/lib.runner[T]).Run"),
		"example.com/fixture/lib.TestPropRapidCheck":       seededReason,
		// Proof outranks property on the ladder and carries its seeding:
		// a direct driver in either order, or a hop through a helper.
		"example.com/fixture/lib.TestProofThenDrive": seededReason,
		"example.com/fixture/lib.TestDriveThenProof": seededReason,
		"example.com/fixture/lib.TestProofViaHelper": seededThroughReason("example.com/fixture/lib.runProp"),
	}
	if !maps.Equal(refused, want) {
		t.Fatalf("NeverServe = %v, want %v", refused, want)
	}
	for _, sym := range symbols[len(symbols)-3:] {
		if got := fb.WitnessClass(sym); got != verify.AnalyzerProof {
			t.Errorf("%s classified %v, want proof — the ladder's top, seeded all the same", sym, got)
		}
	}
	_, reason := fb.WitnessClassVerdict("example.com/fixture/lib.TestPropViaHelper")
	if !strings.Contains(reason, "reached through example.com/fixture/lib.runProp") {
		t.Fatalf("example reason = %q, want the hop named", reason)
	}
}

// The walk answers the same under a scoped load: a helper in an
// in-module package the scope did not hold is loaded on demand, so the
// served backend's per-symbol scope never serves what the whole-tree
// load refuses (REQ-evidence-witness-freshness).
func TestScopedLoadReachesInModuleHelpers(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture module scoped to one package")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	scoped, err := newContext(context.Background(), "testdata/fixturemod", []string{"example.com/fixture/lib"})
	if err != nil {
		t.Fatal(err)
	}
	scoped.walkMu.Lock()
	ownership := scoped.inModule("example.com/fixture/helpers") != "" && scoped.inModule("pgregory.net/rapid") == ""
	scoped.walkMu.Unlock()
	if !ownership {
		t.Fatal("module ownership wrong")
	}
	refused, err := scoped.NeverServe([]string{
		"example.com/fixture/lib.TestPropViaOtherPackage",
		"example.com/fixture/lib.TestPropViaDependencyHelper",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"example.com/fixture/lib.TestPropViaOtherPackage": seededThroughReason("example.com/fixture/helpers.Run")}
	if !maps.Equal(refused, want) {
		t.Fatalf("scoped NeverServe = %v, want %v", refused, want)
	}
}

// The walk fails closed where it has no declaration to read: a call the
// type information cannot resolve (a helper package no view selects),
// and an in-module package that will not load — never a silent serve
// (REQ-evidence-witness-freshness's absence-of-proof rule).
func TestSeedingWalkFailsClosedWithoutADeclaration(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the fixture backend the full tier loads")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	fb := fixtureBackend(t)
	refused, err := fb.NeverServe([]string{
		"example.com/fixture/unresolved.TestViaTagged",
		"example.com/fixture/lib.TestPropViaBadHelper",
	})
	if err != nil {
		t.Fatal(err)
	}
	// A witness whose own package cannot resolve an import refuses
	// through its own load gap, before any walk.
	if why := refused["example.com/fixture/unresolved.TestViaTagged"]; !strings.HasPrefix(why, "unclassifiable witness:") || !strings.Contains(why, "load errors") {
		t.Fatalf("unresolved import served or misattributed: %q", why)
	}
	// A helper whose body calls something undeclared loads with errors
	// but indexes; the walk names the unresolved call.
	if why := refused["example.com/fixture/lib.TestPropViaBadHelper"]; !strings.HasPrefix(why, "unclassifiable seeding:") || !strings.Contains(why, "call of mystery in example.com/fixture/badhelper.Run resolves to no declaration") {
		t.Fatalf("unresolved call in a helper served or misattributed: %q", why)
	}
	// An in-module package whose on-demand load fails refuses serving
	// under the seeding spelling — witnessed by injection: a scoped
	// backend whose lazy configuration points at an empty directory.
	scoped, err := newContext(context.Background(), "testdata/fixturemod", []string{"example.com/fixture/lib"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cfgs := range scoped.lazyCfg {
		for _, cfg := range cfgs {
			cfg.Dir = t.TempDir()
		}
	}
	broken, err := scoped.NeverServe([]string{"example.com/fixture/lib.TestPropViaOtherPackage"})
	if err != nil {
		t.Fatal(err)
	}
	if why := broken["example.com/fixture/lib.TestPropViaOtherPackage"]; !strings.HasPrefix(why, "unclassifiable seeding:") || !strings.Contains(why, "example.com/fixture/helpers") {
		t.Fatalf("failed on-demand load served or misattributed: %q", why)
	}
	// An in-module package path the module does not hold: the on-demand
	// load fails and the walk reports it, never nil.
	pkg := types.NewPackage("example.com/fixture/nosuch", "nosuch")
	ghost := types.NewFunc(token.NoPos, pkg, "Run", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	fb.walkMu.Lock()
	fd, _, err := fb.funcDeclOf(SelectionKey(nil, ""), ghost)
	fb.walkMu.Unlock()
	if err == nil || fd != nil || !strings.Contains(err.Error(), "example.com/fixture/nosuch") {
		t.Fatalf("ghost package: fd=%v err=%v; want a load error naming the package", fd, err)
	}
	// A dependency's function is outside the walk: nil and no error.
	dep := types.NewPackage("pgregory.net/rapid", "rapid")
	fd, _, err = func() (*ast.FuncDecl, *packages.Package, error) {
		fb.walkMu.Lock()
		defer fb.walkMu.Unlock()
		return fb.funcDeclOf(SelectionKey(nil, ""), types.NewFunc(token.NoPos, dep, "Check", types.NewSignatureType(nil, nil, nil, nil, nil, false)))
	}()
	if err != nil || fd != nil {
		t.Fatalf("dependency function: fd=%v err=%v; want the branch to end quietly", fd, err)
	}
}

// The walk runs under each view's own selection: a helper split by
// build tag drives the runner in one view and not the other, and
// serving — one answer for the symbol — refuses because the tagged
// view seeds it, while the default view alone would have served it
// (REQ-evidence-witness-freshness across REQ-go-build-selections).
func TestSeedingWalkIsPerSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture module under two views")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness", "REQ-go-build-selections")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":            "module example.com/split\n\ngo 1.26\n\nrequire pgregory.net/rapid v1.3.0\n",
		"go.sum":            "pgregory.net/rapid v1.3.0 h1:vBvO0VSqti75J1jjYqpgPNBLKMd1+gxa9fYo7vk/Exc=\npgregory.net/rapid v1.3.0/go.mod h1:dPlE4OBBxgXPqkP79flB6sJL1dx5azpI7HQ9MY9Z7uk=\n",
		"lib/lib.go":        "package lib\n\nfunc Add(a, b int) int { return a + b }\n",
		"lib/plain.go":      "//go:build !dst\n\npackage lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc splitDrive(t *testing.T, body func(*rapid.T)) {\n\tif Add(1, 1) != 2 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
		"lib/dst.go":        "//go:build dst\n\npackage lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc splitDrive(t *testing.T, body func(*rapid.T)) {\n\trapid.Check(t, body)\n}\n",
		"lib/split_test.go": "package lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc TestSplit(t *testing.T) {\n\tsplitDrive(t, func(rt *rapid.T) {\n\t\tif Add(2, 2) != 4 {\n\t\t\trt.Fatal(\"broken\")\n\t\t}\n\t})\n}\n",
		// The same symbol declared twice by tag: a plain body in the
		// default view, a DIRECT driver call in the dst view.
		"lib/direct_default_test.go": "//go:build !dst\n\npackage lib\n\nimport \"testing\"\n\nfunc TestSplitDirect(t *testing.T) {\n\tif Add(3, 3) != 6 {\n\t\tt.Fatal(\"broken\")\n\t}\n}\n",
		"lib/direct_dst_test.go":     "//go:build dst\n\npackage lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc TestSplitDirect(t *testing.T) {\n\trapid.Check(t, func(rt *rapid.T) {\n\t\tif Add(3, 3) != 6 {\n\t\t\trt.Fatal(\"broken\")\n\t\t}\n\t})\n}\n",
		// A fuzz target declared twice by tag: a plain harness body in
		// the default view, a rapid driver inside the dst callback —
		// the fuzz classification must not bypass the union.
		"lib/fuzz_default_test.go": "//go:build !dst\n\npackage lib\n\nimport \"testing\"\n\nfunc FuzzThing(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, x int) {\n\t\tif Add(x, 0) != x {\n\t\t\tt.Fatal(\"broken\")\n\t\t}\n\t})\n}\n",
		"lib/fuzz_dst_test.go":     "//go:build dst\n\npackage lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc FuzzThing(f *testing.F) {\n\tf.Fuzz(func(t *testing.T, x int) {\n\t\trapid.Check(t, func(rt *rapid.T) {\n\t\t\tif Add(x, 0) != x {\n\t\t\t\trt.Fatal(\"broken\")\n\t\t\t}\n\t\t})\n\t})\n}\n",
	})
	const symbol = "example.com/split/lib.TestSplit"
	const direct = "example.com/split/lib.TestSplitDirect"
	const fuzz = "example.com/split/lib.FuzzThing"
	plain, err := newContext(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	served, err := plain.NeverServe([]string{symbol, direct, fuzz})
	if err != nil {
		t.Fatal(err)
	}
	if len(served) != 0 {
		t.Fatalf("default view alone refused %v; neither default body drives", served)
	}
	dstPolicy(t, dir)
	both, err := newContext(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := both.NeverServe([]string{symbol, direct, fuzz})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		symbol: seededThroughReason("example.com/split/lib.splitDrive"),
		direct: seededReason,
		fuzz:   seededReason,
	}
	if !maps.Equal(refused, want) {
		t.Fatalf("under the dst view: NeverServe = %v, want %v", refused, want)
	}
	for _, sym := range []string{symbol, direct} {
		if got := both.WitnessClass(sym); got != verify.ExampleWitness {
			t.Fatalf("%s class %v, want the first view's example class", sym, got)
		}
	}
	if got := both.WitnessClass(fuzz); got != verify.PropertyWitness {
		t.Fatalf("fuzz target class %v, want property by harness in every view", got)
	}
}
