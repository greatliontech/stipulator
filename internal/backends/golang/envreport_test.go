package golang

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestGoEnvDivergenceReportContent pins the report's rendering: both
// working directories named, the delivered width read from the spawn
// environment itself (never a re-derivation that could disagree with
// what the process saw), the curated-vs-ambient delta with runner-set
// entries carrying values, both values only for the runner's own pins,
// a declared override's ambient value withheld, dropped entries named
// alone, and the ambient leg being the invocation's normalize-time
// sample.
//
//gofresh:pure
func TestGoEnvDivergenceReportContent(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	n := &NormalizedInvocation{
		Dir:          "/tree/mod",
		EnvOverrides: []string{"SECRET_URL=test-value"},
		Env:          []string{"KEEP=same", "GOMAXPROCS=2", "SECRET_URL=test-value"},
		// WitnessEnv is the spawn's one derivation; the report must
		// consume it rather than re-deriving: the sentinel GOCACHE only
		// present here must appear, and the width line must carry THIS
		// GOMAXPROCS — a narrower pin witnessWidthEnv would have kept —
		// never witnessChildWidth's re-computation.
		WitnessEnv: []string{"KEEP=same", "GOMAXPROCS=2", "SECRET_URL=test-value", "GOCACHE=/pins/gocache"},
		// Ambient is the normalize-time sample the delta diffs against;
		// the sentinel AMBIENT_ONLY exists only here.
		Ambient: []string{"KEEP=same", "GOMAXPROCS=32", "SECRET_URL=postgres://real", "AMBIENT_ONLY=x"},
	}
	rep := envDivergenceReport(n, "/tree/mod/pkg")
	for _, want := range []string{
		"runner execution environment",
		"go test spawned in /tree/mod; the test binary runs in /tree/mod/pkg with PWD pinned to it",
		"delivered parallelism width: GOMAXPROCS=2",
		"GOCACHE=/pins/gocache (runner-set, absent in the ambient environment)",
		"GOMAXPROCS=2 (runner; ambient GOMAXPROCS=32)",
		"SECRET_URL=test-value (declared override; ambient value withheld)",
		"AMBIENT_ONLY dropped by the runner",
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
	if strings.Contains(rep, "postgres://real") {
		t.Error("a declared override's ambient value printed; the override may exist to shadow it")
	}
	if strings.Contains(rep, "AMBIENT_ONLY=x") {
		t.Error("dropped entry's value printed; dropped entries are named alone")
	}
	if strings.Contains(rep, "KEEP") {
		t.Error("an unchanged entry rendered; the report is the delta")
	}
	// Two invocations with different width pins and frozen fan-out
	// bounds must each report THEIR values: a re-derivation computes
	// one host-dependent value and cannot satisfy both, so this pins
	// the width line to the spawn environment and the fan-out to the
	// normalize-time freeze on every host.
	n7 := &NormalizedInvocation{
		Dir:        "/tree/mod",
		Env:        []string{"GOMAXPROCS=7"},
		WitnessEnv: []string{"GOMAXPROCS=7"},
		Ambient:    []string{},
		SpawnBound: 5,
	}
	if rep7 := envDivergenceReport(n7, ""); !strings.Contains(rep7, "delivered parallelism width: GOMAXPROCS=7 (package processes fan out 5 wide)") {
		t.Errorf("width line does not carry the spawn env pin and the frozen fan-out:\n%s", rep7)
	}
	n9 := &NormalizedInvocation{
		Dir:        "/tree/mod",
		Env:        []string{"GOMAXPROCS=3"},
		WitnessEnv: []string{"GOMAXPROCS=3"},
		Ambient:    []string{},
		SpawnBound: 9,
	}
	if rep9 := envDivergenceReport(n9, ""); !strings.Contains(rep9, "delivered parallelism width: GOMAXPROCS=3 (package processes fan out 9 wide)") {
		t.Errorf("width line does not carry the second invocation's frozen values:\n%s", rep9)
	}
}

// TestGoEnvDivergenceReportBoundedAtRender pins the render-time bound:
// an oversized value is named with a bounded prefix and its total size
// — never reproduced — and a pathological key population cuts the
// report at its own cap, so the failure output always keeps most of
// the diagnostic's room (the starvation regimes are unrepresentable,
// not reconciled).
//
//gofresh:pure
func TestGoEnvDivergenceReportBoundedAtRender(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	huge := strings.Repeat("v", 4*envValueCap)
	n := &NormalizedInvocation{
		Dir:          "/tree",
		EnvOverrides: []string{"BIGVAR=" + huge},
		Env:          []string{"BIGVAR=" + huge},
		WitnessEnv:   []string{"BIGVAR=" + huge},
		Ambient:      []string{},
	}
	rep := envDivergenceReport(n, "")
	if strings.Contains(rep, huge) {
		t.Error("an oversized value reproduced in full; values are named with a bounded prefix")
	}
	if !strings.Contains(rep, "[768 bytes total]") {
		t.Errorf("oversized value's total size not named:\n%s", rep)
	}
	var manyKeys []string
	for i := range 2000 {
		manyKeys = append(manyKeys, fmt.Sprintf("RUNNERKEY_%04d=%s", i, strings.Repeat("w", envValueCap)))
	}
	wide := &NormalizedInvocation{
		Dir:        "/tree",
		Env:        manyKeys,
		WitnessEnv: manyKeys,
		Ambient:    []string{},
	}
	wrep := envDivergenceReport(wide, "")
	if len(wrep) > reportCap+64 {
		t.Errorf("report is %d bytes, above its render cap", len(wrep))
	}
	if !strings.Contains(wrep, "[environment report truncated]") {
		t.Error("a report cut at its cap did not say so")
	}
	// The failure output survives in full against the pathological
	// report: enrichment keeps at least three quarters of the
	// diagnostic for the failure text.
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation("inv")
	d.SetPackage("example.com/p")
	d.SetTest("TestFlip")
	d.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	failureText := "the actual assertion failure: " + strings.Repeat("f", failureOutputCap/2)
	d.SetOutput(failureText)
	m := &execMerge{diags: []*stipulatorv1.FailureDiagnostic{d}}
	enrichFlipDiagnostics(m, map[string]*NormalizedInvocation{"inv": wide},
		map[string]bool{"example.com/p.TestFlip": true}, map[string]bool{})
	if !strings.Contains(d.GetOutput(), failureText) {
		t.Error("the failure text was cut by a pathological report; the report is the bounded side")
	}
	if !strings.Contains(d.GetOutput(), "runner execution environment") {
		t.Error("the report absent beside the surviving failure text")
	}
	if len(d.GetOutput()) > failureOutputCap {
		t.Errorf("enriched diagnostic is %d bytes, above the %d cap", len(d.GetOutput()), failureOutputCap)
	}
}

// TestGoEnrichFlipDiagnostics pins the flip gate and the cap: only a
// per-test TEST_FAILED diagnostic whose subject holds prior passing
// evidence gains the report — once per subject even when the isolation
// pass minted a second diagnostic — while a failure with no prior
// pass, a prior FAILURE, a package-scoped diagnostic, and a
// non-failure disposition stay bare; the combined output stays within
// the one-diagnostic cap with truncation marked on the typed field.
//
//gofresh:pure
func TestGoEnrichFlipDiagnostics(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	mk := func(pkg, test string, disp stipulatorv1.HealthDisposition) *stipulatorv1.FailureDiagnostic {
		d := &stipulatorv1.FailureDiagnostic{}
		d.SetInvocation("inv")
		d.SetPackage(pkg)
		d.SetTest(test)
		d.SetDisposition(disp)
		d.SetOutput("original failure output")
		return d
	}
	flip := mk("example.com/p", "TestFlip", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	flipSolo := mk("example.com/p", "TestFlip", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	noPrior := mk("example.com/p", "TestNew", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	priorRed := mk("example.com/p", "TestAlwaysRed", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	pkgScoped := mk("example.com/p", "", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	timeout := mk("example.com/p", "TestOther", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT)
	m := &execMerge{diags: []*stipulatorv1.FailureDiagnostic{flip, flipSolo, noPrior, priorRed, pkgScoped, timeout}}
	normalized := map[string]*NormalizedInvocation{"inv": {
		Dir:        "/tree",
		Env:        []string{"A=1"},
		WitnessEnv: []string{"A=1", "B=2", "GOMAXPROCS=4"},
		Ambient:    []string{"A=1"},
		PkgDirs:    map[string]string{"example.com/p": "/tree/p"},
	}}
	passedBefore := map[string]bool{
		"example.com/p.TestFlip":      true,
		"example.com/p.TestAlwaysRed": false,
		"example.com/p.TestOther":     true,
	}
	enriched := map[string]bool{}
	enrichFlipDiagnostics(m, normalized, passedBefore, enriched)
	if !strings.Contains(flip.GetOutput(), "runner execution environment") || !strings.Contains(flip.GetOutput(), "original failure output") {
		t.Errorf("flip diagnostic not enriched beside its output: %q", flip.GetOutput())
	}
	if !strings.Contains(flip.GetOutput(), "/tree/p") {
		t.Errorf("failing package's directory not named: %q", flip.GetOutput())
	}
	if strings.Contains(flipSolo.GetOutput(), "runner execution environment") {
		t.Error("the same subject's second diagnostic enriched; one report per flipped subject")
	}
	for name, d := range map[string]*stipulatorv1.FailureDiagnostic{
		"no-prior": noPrior, "prior-red": priorRed, "package-scoped": pkgScoped, "non-failure": timeout,
	} {
		if strings.Contains(d.GetOutput(), "runner execution environment") {
			t.Errorf("%s diagnostic enriched; the report rides verdict flips alone", name)
		}
	}
	enrichFlipDiagnostics(nil, normalized, passedBefore, enriched)

	// The dedup spans merges: the same subject under the same invocation
	// in a SECOND merge stays bare, while the same subject under a
	// SECOND invocation failed under a second curated environment and
	// renders its own report.
	crossMerge := mk("example.com/p", "TestFlip", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	otherInv := mk("example.com/p", "TestFlip", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	otherInv.SetInvocation("inv2")
	normalized["inv2"] = &NormalizedInvocation{Dir: "/tree2", Env: []string{"A=1"}, WitnessEnv: []string{"A=1", "GOMAXPROCS=8"}, Ambient: []string{"A=1"}}
	m2 := &execMerge{diags: []*stipulatorv1.FailureDiagnostic{crossMerge, otherInv}}
	enrichFlipDiagnostics(m2, normalized, passedBefore, enriched)
	if strings.Contains(crossMerge.GetOutput(), "runner execution environment") {
		t.Error("the same subject+invocation enriched again in a second merge")
	}
	if !strings.Contains(otherInv.GetOutput(), "runner execution environment") || !strings.Contains(otherInv.GetOutput(), "/tree2") {
		t.Errorf("a second invocation's flip did not render its own environment: %q", otherInv.GetOutput())
	}

	// The one-diagnostic cap holds after enrichment: an output already
	// near the cap gains only what fits, and the typed truncation field
	// records the cut.
	big := mk("example.com/p", "TestFlip", stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	big.SetOutput(strings.Repeat("x", failureOutputCap-10))
	bm := &execMerge{diags: []*stipulatorv1.FailureDiagnostic{big}}
	enrichFlipDiagnostics(bm, normalized, passedBefore, map[string]bool{})
	if len(big.GetOutput()) > failureOutputCap {
		t.Errorf("enriched diagnostic is %d bytes, above the %d cap", len(big.GetOutput()), failureOutputCap)
	}
	if !big.GetTruncated() {
		t.Error("a capped enrichment did not mark the diagnostic truncated")
	}
	// The failure output yields room: the mandated report survives in
	// full even against a near-cap dump — the unbounded side is what
	// gets cut.
	if !strings.Contains(big.GetOutput(), "runner execution environment") || !strings.Contains(big.GetOutput(), "delivered parallelism width") {
		t.Error("a near-cap failure output starved the mandated report")
	}
}

// TestGoEnvDivergenceReportValidUTF8 pins the marshal-integrity bound:
// environment values are bytes, the diagnostic rides an edition-2023
// proto string field that refuses invalid UTF-8 at marshal, so a rune
// straddling the prefix cap, an invalid byte in a short value, and a
// report-cap cut through multi-byte content all render valid — and the
// enriched diagnostic itself marshals.
//
//gofresh:pure
func TestGoEnvDivergenceReportValidUTF8(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	straddle := strings.Repeat("x", envValueCap-1) + "日本語" + strings.Repeat("x", envValueCap)
	invalid := "latin1: caf\xe9"
	n := &NormalizedInvocation{
		Dir:          "/tree",
		EnvOverrides: []string{"STRADDLE=" + straddle, "RAWBYTES=" + invalid},
		Env:          []string{"STRADDLE=" + straddle, "RAWBYTES=" + invalid},
		WitnessEnv:   []string{"STRADDLE=" + straddle, "RAWBYTES=" + invalid},
		Ambient:      []string{},
	}
	rep := envDivergenceReport(n, "")
	if !utf8.ValidString(rep) {
		t.Fatalf("report is not valid UTF-8:\n%q", rep)
	}
	if !strings.Contains(rep, "caf�") {
		t.Errorf("invalid byte not scrubbed to the replacement rune:\n%s", rep)
	}
	// A report-cap cut through multi-byte content stays valid.
	var wide []string
	for i := range 2000 {
		wide = append(wide, fmt.Sprintf("K%04d=%s", i, strings.Repeat("軸", 40)))
	}
	wn := &NormalizedInvocation{Dir: "/tree", Env: wide, WitnessEnv: wide, Ambient: []string{}}
	if wrep := envDivergenceReport(wn, ""); !utf8.ValidString(wrep) {
		t.Fatal("report-cap cut produced invalid UTF-8")
	}
	// The enriched diagnostic marshals: the whole point of the scrub.
	d := &stipulatorv1.FailureDiagnostic{}
	d.SetInvocation("inv")
	d.SetPackage("example.com/p")
	d.SetTest("TestFlip")
	d.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	d.SetOutput("failure text")
	m := &execMerge{diags: []*stipulatorv1.FailureDiagnostic{d}}
	enrichFlipDiagnostics(m, map[string]*NormalizedInvocation{"inv": n},
		map[string]bool{"example.com/p.TestFlip": true}, map[string]bool{})
	if _, err := proto.Marshal(d); err != nil {
		t.Fatalf("enriched diagnostic does not marshal: %v", err)
	}
}

// TestGoTruncateValidUTF8 pins the rune-safe cut directly: a limit
// landing mid-rune backs off to the boundary, an invalid byte scrubs
// to the replacement rune, and the result is always valid UTF-8 —
// the guarantee the proto marshal depends on, independent of any
// outer scrub that would mask a broken inner one.
//
//gofresh:pure
func TestGoTruncateValidUTF8(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-flip-environment")
	if got := truncateValidUTF8("ab日本", 3); got != "ab" {
		t.Errorf("mid-rune cut = %q, want backed off to %q", got, "ab")
	}
	if got := truncateValidUTF8("caf\xe9", 100); got != "caf�" {
		t.Errorf("invalid byte = %q, want scrubbed", got)
	}
	if got := truncateValidUTF8("ab日本", 100); got != "ab日本" {
		t.Errorf("untouched value changed: %q", got)
	}
	// The composition case — invalid bytes AND a cut — is the one that
	// can break the length contract: the replacement rune is wider than
	// the byte it replaces, so scrubbing must precede the cut.
	dirty := strings.Repeat("caf\xe9 ", 100)
	for _, limit := range []int{7, 50, 333} {
		got := truncateValidUTF8(dirty, limit)
		if len(got) > limit {
			t.Errorf("truncateValidUTF8(dirty, %d) is %d bytes — above its own limit", limit, len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncateValidUTF8(dirty, %d) is not valid UTF-8", limit)
		}
	}
}
