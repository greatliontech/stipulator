package coverage

import (
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// fixture compiles a small corpus and loads its records.
func fixture(t *testing.T, doc string, files map[string]string) (*stipulatorv1.Spec, *records.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		"stipulator.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":           {Data: []byte(doc)},
	}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return spec, store
}

func result(id string, role stipulatorv1.BindingRole, pinned bool, res verify.Resolution, shape verify.ShapeState, test verify.TestOutcome) verify.BindingResult {
	return verify.BindingResult{
		Path: "x", RequirementId: id, Symbol: "example.com/p.S", Backend: "go",
		Role: role, ContentPinned: pinned, Resolution: res, Shape: shape, TestOutcome: test,
	}
}

func bucketOf(t *testing.T, rep *Report, id string) Requirement {
	t.Helper()
	for _, r := range rep.Requirements {
		if r.Id == id {
			return r
		}
	}
	t.Fatalf("requirement %s not in report", id)
	return Requirement{}
}

const (
	impl  = stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS
	tests = stipulatorv1.BindingRole_BINDING_ROLE_TESTS
)

// TestPolicyDefaults pins the (kind, keyword) → minimum-evidence table and
// with it the evidence ladder: a witness satisfies behavior, a static
// binding does not; a static binding satisfies SHOULD; nothing satisfies
// structural until an analyzer proof exists.
func TestPolicyDefaults(t *testing.T) {
	stipulate.Covers(t, "REQ-coverage-policy-default", "REQ-evidence-ladder")
	doc := "# T\n\n" +
		"**REQ-c-beh** (behavior): It MUST x.\n\n" +
		"**REQ-c-inv** (invariant): It MUST hold.\n\n" +
		"**REQ-c-str** (structural): It MUST NOT depend.\n\n" +
		"**REQ-c-wire** (wire): It MUST encode.\n\n" +
		"**REQ-c-should** (behavior): It SHOULD y.\n\n" +
		"**REQ-c-may** (behavior): It MAY z.\n\n" +
		"**REQ-c-mayfree** (behavior): It MAY w.\n"
	spec, store := fixture(t, doc, nil)

	witness := func(id string) verify.BindingResult {
		return result(id, tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	}
	static := func(id string) verify.BindingResult {
		return result(id, impl, true, verify.Resolved, verify.ShapeMatch, verify.TestNotRun)
	}
	vr := &verify.Report{Results: []verify.BindingResult{
		witness("REQ-c-beh"),
		witness("REQ-c-inv"),
		witness("REQ-c-str"), // witness is NOT enough for structural
		witness("REQ-c-wire"),
		static("REQ-c-should"),
		static("REQ-c-may"),
	}}
	rep := Evaluate(spec, vr, store, true)
	want := map[string]Bucket{
		"REQ-c-beh":     Covered,
		"REQ-c-inv":     Covered,
		"REQ-c-str":     Uncovered, // needs analyzer proof
		"REQ-c-wire":    Covered,
		"REQ-c-should":  Covered,
		"REQ-c-may":     Covered,
		"REQ-c-mayfree": Exempt, // unbound MAY
	}
	for id, w := range want {
		if got := bucketOf(t, rep, id).Bucket; got != w {
			t.Errorf("%s = %v, want %v", id, got, w)
		}
	}

	// A static binding alone never satisfies a MUST behavior.
	vr2 := &verify.Report{Results: []verify.BindingResult{static("REQ-c-beh")}}
	if got := bucketOf(t, Evaluate(spec, vr2, store, true), "REQ-c-beh").Bucket; got != Uncovered {
		t.Errorf("static-only MUST behavior = %v, want uncovered", got)
	}
}

// TestBuckets pins precedence and claim hygiene: broken beats stale beats
// covered — red claims downgrade even when other evidence satisfies.
func TestBuckets(t *testing.T) {
	stipulate.Covers(t, "REQ-coverage-buckets")
	doc := "# T\n\n**REQ-c-a** (behavior): It MUST x.\n"
	spec, store := fixture(t, doc, nil)
	witness := result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)

	cases := []struct {
		name  string
		extra verify.BindingResult
		want  Bucket
	}{
		{"witness alone covers", witness, Covered},
		{"broken symbol downgrades", result("REQ-c-a", impl, true, verify.NotFound, verify.ShapeUnknown, verify.TestNotRun), Broken},
		{"shape mismatch downgrades", result("REQ-c-a", impl, true, verify.Resolved, verify.ShapeMismatch, verify.TestNotRun), Broken},
		{"failed bound test downgrades", result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestFailed), Broken},
		{"unwitnessed bound test downgrades", result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestNotRun), Broken},
		{"stale content pin downgrades", result("REQ-c-a", impl, false, verify.Resolved, verify.ShapeMatch, verify.TestNotRun), Stale},
		{"unpinned shape downgrades", result("REQ-c-a", impl, true, verify.Resolved, verify.ShapeUnpinned, verify.TestNotRun), Stale},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vr := &verify.Report{Results: []verify.BindingResult{witness, c.extra}}
			if got := bucketOf(t, Evaluate(spec, vr, store, true), "REQ-c-a").Bucket; got != c.want {
				t.Fatalf("bucket = %v, want %v", got, c.want)
			}
		})
	}

	// Broken beats stale when both present.
	vr := &verify.Report{Results: []verify.BindingResult{
		result("REQ-c-a", impl, false, verify.Resolved, verify.ShapeMatch, verify.TestNotRun),
		result("REQ-c-a", impl, true, verify.NotFound, verify.ShapeUnknown, verify.TestNotRun),
	}}
	if got := bucketOf(t, Evaluate(spec, vr, store, true), "REQ-c-a").Bucket; got != Broken {
		t.Fatalf("broken+stale = %v, want broken", got)
	}
}

// TestClaimsGrantNothingUnverified pins the trust boundary: pinned claims
// with no verifying backend, and passed tests without witnessing, grant no
// evidence tier.
func TestClaimsGrantNothingUnverified(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-promotion", "REQ-core-claims-untrusted")
	doc := "# T\n\n**REQ-c-a** (behavior): It MUST x.\n"
	spec, store := fixture(t, doc, nil)

	// Fully pinned but unverified (no backend ran): no evidence.
	unverified := result("REQ-c-a", tests, true, verify.Unverified, verify.ShapeUnknown, verify.TestPassed)
	rep := Evaluate(spec, &verify.Report{Results: []verify.BindingResult{unverified}}, store, true)
	if got := bucketOf(t, rep, "REQ-c-a").Bucket; got != Uncovered {
		t.Fatalf("unverified claim granted evidence: %v", got)
	}

	// Resolved and passing, but the run was not witnessed: no witness tier.
	resolved := result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	rep = Evaluate(spec, &verify.Report{Results: []verify.BindingResult{resolved}}, store, false)
	if got := bucketOf(t, rep, "REQ-c-a").Bucket; got != Uncovered {
		t.Fatalf("unwitnessed run granted a witness: %v", got)
	}
}

// TestGapStates pins the gap lifecycle and landing conditions.
func TestGapStates(t *testing.T) {
	stipulate.Covers(t, "REQ-gap-lifecycle", "REQ-gap-conditions")
	doc := "# T\n\n**REQ-c-a** (behavior): It MUST x.\n\n**REQ-c-b** (behavior): It MUST y.\n\n**REQ-c-c** (behavior): It MUST z.\n\n**REQ-c-d** (behavior): It MUST w.\n"
	gap := func(id, lands string) string {
		return "requirement_id: \"" + id + "\"\nreason: \"r\"\nlands { " + lands + " }\n"
	}
	spec, store := fixture(t, doc, map[string]string{
		".stipulator/gaps/a.textproto": gap("REQ-c-a", `covered: "REQ-c-d"`),      // due when d covered
		".stipulator/gaps/b.textproto": gap("REQ-c-b", `exists: "REQ-c-ghost"`),   // open: target absent
		".stipulator/gaps/c.textproto": gap("REQ-c-c", `attested { condition: "external" fired: true }`), // due: fired
		".stipulator/gaps/d.textproto": gap("REQ-c-d", `exists: "REQ-c-a"`),       // resolved: d covered
	})
	vr := &verify.Report{Results: []verify.BindingResult{
		result("REQ-c-d", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed),
	}}
	rep := Evaluate(spec, vr, store, true)
	want := map[string]GapState{
		"REQ-c-a": Due, "REQ-c-b": Open, "REQ-c-c": Due, "REQ-c-d": Resolved,
	}
	for _, g := range rep.Gaps {
		if w, ok := want[g.RequirementId]; !ok || g.State != w {
			t.Errorf("gap %s = %v, want %v", g.RequirementId, g.State, want[g.RequirementId])
		}
	}
	if !rep.GatePasses() {
		t.Fatalf("all reds gapped, yet gate fails: %v", rep.Violations)
	}
}

// TestGate pins REQ-gate-no-undeclared exactly, and REQ-coverage-no-scalar
// with it: verdicts follow the red-without-gap set, never any aggregate
// ratio — one undeclared red among many covered fails; all-red-all-gapped
// passes.
func TestGate(t *testing.T) {
	stipulate.Covers(t, "REQ-gate-no-undeclared", "REQ-coverage-no-scalar")
	doc := "# T\n\n**REQ-c-a** (behavior): It MUST x.\n\n**REQ-c-b** (behavior): It MUST y.\n"

	t.Run("one undeclared red fails despite high coverage", func(t *testing.T) {
		spec, store := fixture(t, doc, nil)
		vr := &verify.Report{Results: []verify.BindingResult{
			result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed),
		}}
		rep := Evaluate(spec, vr, store, true)
		if rep.GatePasses() || len(rep.Violations) != 1 || rep.Violations[0] != "REQ-c-b" {
			t.Fatalf("violations = %v", rep.Violations)
		}
	})
	t.Run("zero coverage passes when every red is declared", func(t *testing.T) {
		gap := func(id string) string {
			return "requirement_id: \"" + id + "\"\nreason: \"r\"\nlands { attested { condition: \"later\" } }\n"
		}
		spec2, store2 := fixture(t, doc, map[string]string{
			".stipulator/gaps/a.textproto": gap("REQ-c-a"),
			".stipulator/gaps/b.textproto": gap("REQ-c-b"),
		})
		rep := Evaluate(spec2, &verify.Report{}, store2, true)
		if !rep.GatePasses() {
			t.Fatalf("declared reds failed the gate: %v", rep.Violations)
		}
	})
}

func TestReasonsAreDeterministic(t *testing.T) {
	doc := "# T\n\n**REQ-c-a** (behavior): It MUST x.\n"
	spec, store := fixture(t, doc, nil)
	vr := &verify.Report{Results: []verify.BindingResult{
		result("REQ-c-a", impl, false, verify.NotFound, verify.ShapeUnknown, verify.TestNotRun),
	}}
	a := bucketOf(t, Evaluate(spec, vr, store, true), "REQ-c-a")
	b := bucketOf(t, Evaluate(spec, vr, store, true), "REQ-c-a")
	if strings.Join(a.Reasons, "|") != strings.Join(b.Reasons, "|") {
		t.Fatal("reasons order unstable")
	}
	if len(a.Reasons) == 0 {
		t.Fatal("red bucket carries no reasons")
	}
}
