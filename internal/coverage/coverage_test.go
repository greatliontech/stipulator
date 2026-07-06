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
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
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
// binding does not; a static binding satisfies SHOULD; structural
// accepts nothing weaker than an analyzer proof.
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
	property := func(id string) verify.BindingResult {
		r := witness(id)
		r.WitnessClass = verify.PropertyWitness
		return r
	}
	static := func(id string) verify.BindingResult {
		return result(id, impl, true, verify.Resolved, verify.ShapeMatch, verify.TestNotRun)
	}
	vr := &verify.Report{Results: []verify.BindingResult{
		witness("REQ-c-beh"),
		witness("REQ-c-inv"), // example witness is NOT enough for an invariant
		witness("REQ-c-str"), // witness is NOT enough for structural
		witness("REQ-c-wire"),
		static("REQ-c-should"),
		static("REQ-c-may"),
	}}
	rep := Evaluate(spec, vr, store, true, nil)
	want := map[string]Bucket{
		"REQ-c-beh":     Covered,
		"REQ-c-inv":     Uncovered, // for-all claim wants a for-all witness
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
	if got := bucketOf(t, Evaluate(spec, vr2, store, true, nil), "REQ-c-beh").Bucket; got != Uncovered {
		t.Errorf("static-only MUST behavior = %v, want uncovered", got)
	}

	// A property witness satisfies an invariant — and a behavior too
	// (stronger satisfies weaker).
	vr3 := &verify.Report{Results: []verify.BindingResult{property("REQ-c-inv"), property("REQ-c-beh")}}
	rep3 := Evaluate(spec, vr3, store, true, nil)
	if got := bucketOf(t, rep3, "REQ-c-inv").Bucket; got != Covered {
		t.Errorf("property witness on invariant = %v, want covered", got)
	}
	if got := bucketOf(t, rep3, "REQ-c-beh").Bucket; got != Covered {
		t.Errorf("property witness on behavior = %v, want covered", got)
	}

	// The SHOULD row admits an attestation by default ("a static binding
	// or an attestation") — rendered attested, never covered; a MUST row
	// admits none.
	att := func(id string) verify.AttestationResult {
		return verify.AttestationResult{RequirementId: id, Reason: "judged", ContentPinned: true}
	}
	vr4 := &verify.Report{Attestations: []verify.AttestationResult{att("REQ-c-should"), att("REQ-c-beh")}}
	rep4 := Evaluate(spec, vr4, store, true, nil)
	if got := bucketOf(t, rep4, "REQ-c-should").Bucket; got != Attested {
		t.Errorf("attested SHOULD = %v, want attested", got)
	}
	if got := bucketOf(t, rep4, "REQ-c-beh").Bucket; got != Uncovered {
		t.Errorf("attested MUST = %v, want uncovered", got)
	}
}

// TestPolicyOverrides pins the manifest override surface: a named cell's
// satisfaction set replaces the default exactly there, unnamed cells keep
// the defaults, exempt cells mirror the MAY rule while red claims still
// read red, and the uncovered reason names the override.
func TestPolicyOverrides(t *testing.T) {
	stipulate.Covers(t, "REQ-coverage-policy")
	doc := "# T\n\n" +
		"**REQ-c-str** (structural): It MUST NOT depend.\n\n" +
		"**REQ-c-beh** (behavior): It MUST x.\n\n" +
		"**REQ-c-inv** (invariant): It MUST hold.\n\n" +
		"**REQ-c-ex** (behavior): It MUST NOT y.\n"
	spec, store := fixture(t, doc, nil)

	manifest := &stipulatorv1.Manifest{}
	override := func(kind stipulatorv1.ClauseKind, kw stipulatorv1.Keyword, min stipulatorv1.MinimumEvidence) *stipulatorv1.PolicyOverride {
		o := &stipulatorv1.PolicyOverride{}
		o.SetKind(kind)
		o.SetKeyword(kw)
		o.SetMinimum(min)
		return o
	}
	manifest.SetPolicy([]*stipulatorv1.PolicyOverride{
		override(stipulatorv1.ClauseKind_CLAUSE_KIND_STRUCTURAL, stipulatorv1.Keyword_KEYWORD_MUST_NOT,
			stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_PROOF_OR_WITNESS),
		override(stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR, stipulatorv1.Keyword_KEYWORD_MUST,
			stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_STATIC),
		override(stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR, stipulatorv1.Keyword_KEYWORD_MUST_NOT,
			stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_EXEMPT),
	})
	pol, err := PolicyFromManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}

	witness := result("REQ-c-str", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	static := result("REQ-c-beh", impl, true, verify.Resolved, verify.ShapeMatch, verify.TestNotRun)
	vr := &verify.Report{Results: []verify.BindingResult{witness, static}}
	rep := Evaluate(spec, vr, store, true, pol)

	// The overridden structural cell accepts a witness; the overridden
	// behavior cell accepts a static binding.
	if got := bucketOf(t, rep, "REQ-c-str").Bucket; got != Covered {
		t.Errorf("overridden structural with witness = %v, want covered", got)
	}
	if got := bucketOf(t, rep, "REQ-c-beh").Bucket; got != Covered {
		t.Errorf("overridden behavior with static = %v, want covered", got)
	}
	// The unnamed invariant cell keeps its default: uncovered, with the
	// default reason.
	inv := bucketOf(t, rep, "REQ-c-inv")
	if inv.Bucket != Uncovered {
		t.Errorf("unnamed cell = %v, want uncovered", inv.Bucket)
	}
	// An exempt cell reads exempt when unbound.
	if got := bucketOf(t, rep, "REQ-c-ex").Bucket; got != Exempt {
		t.Errorf("exempt cell unbound = %v, want exempt", got)
	}

	// Without the overrides the same evidence covers neither cell, and
	// the reason names the override when they are on.
	repDefault := Evaluate(spec, vr, store, true, nil)
	if got := bucketOf(t, repDefault, "REQ-c-str").Bucket; got != Uncovered {
		t.Errorf("default structural with witness = %v, want uncovered", got)
	}
	if got := bucketOf(t, repDefault, "REQ-c-ex").Bucket; got != Uncovered {
		t.Errorf("default MUST NOT unbound = %v, want uncovered", got)
	}
	weak := result("REQ-c-str", impl, true, verify.Resolved, verify.ShapeMatch, verify.TestNotRun)
	repWeak := Evaluate(spec, &verify.Report{Results: []verify.BindingResult{weak}}, store, true, pol)
	r := bucketOf(t, repWeak, "REQ-c-str")
	if r.Bucket != Uncovered || !strings.Contains(strings.Join(r.Reasons, " "), "manifest override") {
		t.Errorf("override reason missing: %v %v", r.Bucket, r.Reasons)
	}

	// Claim hygiene survives an exempt cell: a broken binding on it
	// still reads broken.
	brokenEx := result("REQ-c-ex", impl, true, verify.NotFound, verify.ShapeUnknown, verify.TestNotRun)
	repBroken := Evaluate(spec, &verify.Report{Results: []verify.BindingResult{brokenEx}}, store, true, pol)
	if got := bucketOf(t, repBroken, "REQ-c-ex").Bucket; got != Broken {
		t.Errorf("broken claim on exempt cell = %v, want broken", got)
	}

	// Contract-tier configuration is surfaced: every active override
	// appears in the report, canonically ordered, and on the wire.
	if len(rep.PolicyOverrides) != 3 {
		t.Fatalf("overrides surfaced = %v", rep.PolicyOverrides)
	}
	joined := strings.Join(rep.PolicyOverrides, "\n")
	for _, want := range []string{"(structural, must not) -> proof_or_witness", "(behavior, must) -> static", "(behavior, must not) -> exempt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("override %q not surfaced:\n%s", want, joined)
		}
	}
	if got := rep.Proto().GetPolicyOverrides(); len(got) != 3 {
		t.Errorf("wire overrides = %v", got)
	}
	if repDefault.PolicyOverrides != nil {
		t.Errorf("default policy surfaced overrides: %v", repDefault.PolicyOverrides)
	}

	// A self-contradictory policy is refused, never entry-order resolved.
	manifest.SetPolicy(append(manifest.GetPolicy(),
		override(stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR, stipulatorv1.Keyword_KEYWORD_MUST,
			stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_WITNESS)))
	if _, err := PolicyFromManifest(manifest); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate cell accepted: %v", err)
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
			if got := bucketOf(t, Evaluate(spec, vr, store, true, nil), "REQ-c-a").Bucket; got != c.want {
				t.Fatalf("bucket = %v, want %v", got, c.want)
			}
		})
	}

	// Broken beats stale when both present.
	vr := &verify.Report{Results: []verify.BindingResult{
		result("REQ-c-a", impl, false, verify.Resolved, verify.ShapeMatch, verify.TestNotRun),
		result("REQ-c-a", impl, true, verify.NotFound, verify.ShapeUnknown, verify.TestNotRun),
	}}
	if got := bucketOf(t, Evaluate(spec, vr, store, true, nil), "REQ-c-a").Bucket; got != Broken {
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
	rep := Evaluate(spec, &verify.Report{Results: []verify.BindingResult{unverified}}, store, true, nil)
	if got := bucketOf(t, rep, "REQ-c-a").Bucket; got != Uncovered {
		t.Fatalf("unverified claim granted evidence: %v", got)
	}

	// Resolved and passing, but the run was not witnessed: no witness tier.
	resolved := result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	rep = Evaluate(spec, &verify.Report{Results: []verify.BindingResult{resolved}}, store, false, nil)
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
		".stipulator/gaps/a.textproto": gap("REQ-c-a", `covered: "REQ-c-d"`),                           // due when d covered
		".stipulator/gaps/b.textproto": gap("REQ-c-b", `exists: "REQ-c-ghost"`),                        // open: target absent
		".stipulator/gaps/c.textproto": gap("REQ-c-c", `manual { condition: "external" fired: true }`), // due: fired
		".stipulator/gaps/d.textproto": gap("REQ-c-d", `exists: "REQ-c-a"`),                            // resolved: d covered
	})
	vr := &verify.Report{Results: []verify.BindingResult{
		result("REQ-c-d", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed),
	}}
	rep := Evaluate(spec, vr, store, true, nil)
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
	// Registered per subtest, not at the top: each arm pins both clauses
	// on its own, and the live corpus should exercise the
	// subtest-granular registration path it ships to consumers.
	doc := "# T\n\n**REQ-c-a** (behavior): It MUST x.\n\n**REQ-c-b** (behavior): It MUST y.\n"

	t.Run("one undeclared red fails despite high coverage", func(t *testing.T) {
		stipulate.Covers(t, "REQ-gate-no-undeclared", "REQ-coverage-no-scalar")
		spec, store := fixture(t, doc, nil)
		vr := &verify.Report{Results: []verify.BindingResult{
			result("REQ-c-a", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed),
		}}
		rep := Evaluate(spec, vr, store, true, nil)
		if rep.GatePasses() || len(rep.Violations) != 1 || rep.Violations[0] != "REQ-c-b" {
			t.Fatalf("violations = %v", rep.Violations)
		}
	})
	t.Run("zero coverage passes when every red is declared", func(t *testing.T) {
		stipulate.Covers(t, "REQ-gate-no-undeclared", "REQ-coverage-no-scalar")
		gap := func(id string) string {
			return "requirement_id: \"" + id + "\"\nreason: \"r\"\nlands { manual { condition: \"later\" } }\n"
		}
		spec2, store2 := fixture(t, doc, map[string]string{
			".stipulator/gaps/a.textproto": gap("REQ-c-a"),
			".stipulator/gaps/b.textproto": gap("REQ-c-b"),
		})
		rep := Evaluate(spec2, &verify.Report{}, store2, true, nil)
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
	a := bucketOf(t, Evaluate(spec, vr, store, true, nil), "REQ-c-a")
	b := bucketOf(t, Evaluate(spec, vr, store, true, nil), "REQ-c-a")
	if strings.Join(a.Reasons, "|") != strings.Join(b.Reasons, "|") {
		t.Fatal("reasons order unstable")
	}
	if len(a.Reasons) == 0 {
		t.Fatal("red bucket carries no reasons")
	}
}

// TestAnalyzerProofSatisfiesStructural pins the proof grant: a passing
// proves-role binding whose witness class is an analyzer proof covers
// structural (and invariant) requirements; an example witness never does.
func TestAnalyzerProofSatisfiesStructural(t *testing.T) {
	stipulate.Covers(t, "REQ-go-structural-provers")
	doc := "# T\n\n**REQ-c-str** (structural): It MUST NOT depend.\n\n**REQ-c-inv** (invariant): It MUST hold.\n\n**REQ-c-beh** (behavior): It MUST behave.\n"
	spec, store := fixture(t, doc, nil)
	proves := stipulatorv1.BindingRole_BINDING_ROLE_PROVES

	proof := result("REQ-c-str", proves, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	proof.WitnessClass = verify.AnalyzerProof
	invProof := result("REQ-c-inv", proves, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	invProof.WitnessClass = verify.AnalyzerProof
	rep := Evaluate(spec, &verify.Report{Results: []verify.BindingResult{proof, invProof}}, store, true, nil)
	if b := bucketOf(t, rep, "REQ-c-str"); b.Bucket != Covered {
		t.Fatalf("structural with proof = %s (%v)", b.Bucket, b.Reasons)
	}
	if b := bucketOf(t, rep, "REQ-c-inv"); b.Bucket != Covered {
		t.Fatalf("invariant with proof = %s (%v)", b.Bucket, b.Reasons)
	}

	// The same binding downgraded to an example witness covers neither.
	weak := proof
	weak.WitnessClass = verify.ExampleWitness
	weakInv := invProof
	weakInv.WitnessClass = verify.ExampleWitness
	rep = Evaluate(spec, &verify.Report{Results: []verify.BindingResult{weak, weakInv}}, store, true, nil)
	if b := bucketOf(t, rep, "REQ-c-str"); b.Bucket != Uncovered {
		t.Fatalf("structural with example witness = %s", b.Bucket)
	}
	// A failing prover is broken, not merely uncovered.
	failing := proof
	failing.TestOutcome = verify.TestFailed
	rep = Evaluate(spec, &verify.Report{Results: []verify.BindingResult{failing}}, store, true, nil)
	if b := bucketOf(t, rep, "REQ-c-str"); b.Bucket != Broken {
		t.Fatalf("failing prover = %s", b.Bucket)
	}

	// A proves claim whose class drifted off proof grants nothing at all:
	// even a behavior requirement, which an example witness would cover,
	// stays uncovered.
	drifted := result("REQ-c-beh", proves, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	drifted.WitnessClass = verify.ExampleWitness
	rep = Evaluate(spec, &verify.Report{Results: []verify.BindingResult{drifted}}, store, true, nil)
	beh := bucketOf(t, rep, "REQ-c-beh")
	if beh.Bucket != Uncovered {
		t.Fatalf("behavior with drifted proves claim = %s", beh.Bucket)
	}
	// The report names the drift, not just the missing evidence.
	if !strings.Contains(strings.Join(beh.Reasons, "; "), "no longer classifies as an analyzer proof") {
		t.Fatalf("drift not named in reasons: %v", beh.Reasons)
	}
}

// TestAttestationEvidence pins the weakest rung (REQ-evidence-attestation):
// an attestation covers nothing by default, renders the distinct attested
// bucket only where a policy cell admits it, carries its reason into the
// output, is never folded into covered when stronger evidence exists, and
// stales when the requirement moves.
func TestAttestationEvidence(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-attestation")
	doc := "# T\n\n**REQ-c-att** (behavior): It MUST x.\n\n**REQ-c-plain** (behavior): It MUST y.\n"
	spec, store := fixture(t, doc, nil)

	manifest := &stipulatorv1.Manifest{}
	o := &stipulatorv1.PolicyOverride{}
	o.SetKind(stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR)
	o.SetKeyword(stipulatorv1.Keyword_KEYWORD_MUST)
	o.SetMinimum(stipulatorv1.MinimumEvidence_MINIMUM_EVIDENCE_ATTESTATION)
	manifest.SetPolicy([]*stipulatorv1.PolicyOverride{o})
	pol, err := PolicyFromManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}

	att := verify.AttestationResult{RequirementId: "REQ-c-att", Reason: "judged by review", ContentPinned: true}

	// Admitted: the distinct bucket, reason carried, never a violation.
	rep := Evaluate(spec, &verify.Report{Attestations: []verify.AttestationResult{att}}, store, true, pol)
	r := bucketOf(t, rep, "REQ-c-att")
	if r.Bucket != Attested {
		t.Fatalf("admitted attestation = %v, want attested", r.Bucket)
	}
	if !strings.Contains(strings.Join(r.Reasons, " "), "attested: judged by review") {
		t.Fatalf("reason not carried: %v", r.Reasons)
	}
	if bucketProto[r.Bucket] != stipulatorv1.Bucket_BUCKET_ATTESTED {
		t.Fatalf("wire bucket = %v", bucketProto[r.Bucket])
	}
	for _, v := range rep.Violations {
		if v == "REQ-c-att" {
			t.Fatal("attested requirement read as a violation")
		}
	}

	// Never aggregated: with a real witness present the bucket is
	// covered — the attestation contributes nothing to stronger kinds.
	wit := result("REQ-c-att", tests, true, verify.Resolved, verify.ShapeMatch, verify.TestPassed)
	rep2 := Evaluate(spec, &verify.Report{
		Results:      []verify.BindingResult{wit},
		Attestations: []verify.AttestationResult{att},
	}, store, true, pol)
	if got := bucketOf(t, rep2, "REQ-c-att").Bucket; got != Covered {
		t.Fatalf("witness + attestation = %v, want covered", got)
	}

	// Default policy admits attestation nowhere: uncovered, with the
	// inadmissibility surfaced.
	rep3 := Evaluate(spec, &verify.Report{Attestations: []verify.AttestationResult{att}}, store, true, nil)
	r3 := bucketOf(t, rep3, "REQ-c-att")
	if r3.Bucket != Uncovered {
		t.Fatalf("default-policy attestation = %v, want uncovered", r3.Bucket)
	}
	if !strings.Contains(strings.Join(r3.Reasons, " "), "does not meet this cell's minimum") {
		t.Fatalf("inadmissibility not surfaced: %v", r3.Reasons)
	}

	// A moved requirement stales the voucher.
	staleAtt := verify.AttestationResult{RequirementId: "REQ-c-att", Reason: "old judgment", ContentPinned: false}
	rep4 := Evaluate(spec, &verify.Report{Attestations: []verify.AttestationResult{staleAtt}}, store, true, pol)
	if got := bucketOf(t, rep4, "REQ-c-att").Bucket; got != Stale {
		t.Fatalf("stale attestation = %v, want stale", got)
	}
}
