package verify

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
)

const goodDoc = "# T\n\n**REQ-v-a** (behavior): It MUST x.\n\n**REQ-v-b** (behavior): It MUST y.\n"

func run(t *testing.T, files map[string]string) (*Report, *records.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(goodDoc)},
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
	return Run(spec, store, nil, nil), store
}

func wantProblem(t *testing.T, rep *Report, substr string) {
	t.Helper()
	for _, p := range rep.Problems {
		if strings.Contains(p.Message, substr) {
			return
		}
	}
	t.Fatalf("no problem containing %q in %v", substr, rep.Problems)
}

func binding(id, hash string) string {
	b := "bindings {\n  requirement_id: \"" + id + "\"\n"
	if hash != "" {
		b += "  content_hash: \"" + hash + "\"\n"
	}
	return b + "  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n"
}

//gofresh:pure
func TestConsistency(t *testing.T) {
	t.Run("dangling binding", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/bindings/x.textproto": binding("REQ-v-ghost", ""),
		})
		wantProblem(t, rep, "names REQ-v-ghost, which is not in the corpus — unbind it: stipulator unbind --req REQ-v-ghost (or stipulator dispose retire --id REQ-v-ghost if the requirement was removed deliberately)")
	})
	t.Run("dangling attestation names its retraction repair", func(t *testing.T) {
		stipulate.Covers(t, "REQ-change-remediation")
		rep, _ := run(t, map[string]string{
			".stipulator/attestations/ghost.textproto": "attestations {\n  requirement_id: \"REQ-v-ghost\"\n  reason: \"r\"\n}\n",
		})
		wantProblem(t, rep, "attestation names REQ-v-ghost, which is not in the corpus — retract it: stipulator attest requirement --req REQ-v-ghost --retract")
	})
	t.Run("dangling gap names its retraction repair", func(t *testing.T) {
		stipulate.Covers(t, "REQ-gap-retract")
		rep, _ := run(t, map[string]string{
			".stipulator/gaps/ghost.textproto": "requirement_id: \"REQ-v-ghost\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n",
		})
		wantProblem(t, rep, "gap names REQ-v-ghost, which is not in the corpus — retract it: stipulator gap --req REQ-v-ghost --retract (or prune --dangling for the bulk repair)")
	})
	t.Run("unset pin is stale, current pin is pinned", func(t *testing.T) {
		rep, store := run(t, map[string]string{
			".stipulator/bindings/x.textproto": binding("REQ-v-a", ""),
		})
		if len(rep.Problems) != 0 || rep.Stale != 1 || rep.Pinned != 0 {
			t.Fatalf("problems=%v stale=%d pinned=%d", rep.Problems, rep.Stale, rep.Pinned)
		}
		_ = store
	})
	t.Run("mismatched pin is stale", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/bindings/x.textproto": binding("REQ-v-a", strings.Repeat("0", 64)),
		})
		if rep.Stale != 1 {
			t.Fatalf("stale=%d", rep.Stale)
		}
	})
	t.Run("malformed binding fields", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/bindings/x.textproto": "bindings { requirement_id: \"REQ-v-a\" }\n",
		})
		wantProblem(t, rep, "has no backend")
		wantProblem(t, rep, "has no symbol")
		wantProblem(t, rep, "has no role")
	})
	t.Run("dangling gap and missing fields", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/gaps/x.textproto": "requirement_id: \"REQ-v-ghost\"\n",
		})
		wantProblem(t, rep, "gap names REQ-v-ghost")
		wantProblem(t, rep, "has no reason")
		wantProblem(t, rep, "has no landing condition")
	})
	t.Run("well-formed gap with prospective condition is clean", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/gaps/x.textproto": "requirement_id: \"REQ-v-a\"\nreason: \"backend pending\"\nlands { exists: \"REQ-v-future\" }\n",
		})
		if len(rep.Problems) != 0 {
			t.Fatalf("problems = %v", rep.Problems)
		}
	})
}

//gofresh:pure
func TestPin(t *testing.T) {
	header := "# proto-file: proto/stipulator/v1/records.proto\n# proto-message: stipulator.v1.BindingSet\n"
	rep, store := run(t, map[string]string{
		".stipulator/bindings/x.textproto": header + binding("REQ-v-a", "") + binding("REQ-v-ghost", ""),
	})
	_ = rep
	hashes := map[string]string{"REQ-v-a": strings.Repeat("a", 64)}
	updates, err := records.Pin(store, hashes, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := updates[".stipulator/bindings/x.textproto"]
	if !ok {
		t.Fatal("no update produced")
	}
	s := string(got)
	if !strings.HasPrefix(s, header) {
		t.Fatalf("header not preserved:\n%s", s)
	}
	if !strings.Contains(s, "content_hash: \""+strings.Repeat("a", 64)+"\"") {
		t.Fatalf("pin not written:\n%s", s)
	}
	if strings.Contains(strings.Split(s, "REQ-v-ghost")[1], "content_hash") {
		t.Fatalf("unknown requirement got a pin:\n%s", s)
	}
	// A differing content pin is NEVER rewritten by pin — that is an
	// editorial disposition. (REQ-pin-backfill)
	store3, _ := records.Load(fstest.MapFS{
		".stipulator/bindings/x.textproto": {Data: []byte(binding("REQ-v-a", strings.Repeat("0", 64)))},
	})
	if ups, err := records.Pin(store3, hashes, nil); err != nil || len(ups) != 0 {
		t.Fatalf("differing pin laundered by pin: %v %v", ups, err)
	}

	// Deterministic: pinning twice produces identical bytes.
	store2, _ := records.Load(fstest.MapFS{
		".stipulator/bindings/x.textproto": {Data: got},
	})
	if again, err := records.Pin(store2, hashes, nil); err != nil || len(again) != 0 {
		t.Fatalf("re-pin of pinned file produced changes: %v %v", again, err)
	}
}

//gofresh:pure
func TestPinRefusesCommentedFile(t *testing.T) {
	header := "# proto-file: proto/stipulator/v1/records.proto\n"
	_, store := run(t, map[string]string{
		".stipulator/bindings/x.textproto": header + binding("REQ-v-a", "") +
			"# reviewed by hand, keep\n" + binding("REQ-v-b", ""),
	})
	_, err := records.Pin(store, map[string]string{
		"REQ-v-a": strings.Repeat("a", 64),
		"REQ-v-b": strings.Repeat("b", 64),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "comment outside the leading header") {
		t.Fatalf("want comment refusal, got %v", err)
	}
}

//gofresh:pure
func TestRecordHygiene(t *testing.T) {
	t.Run("duplicate binding flagged", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/bindings/x.textproto": binding("REQ-v-a", "") + binding("REQ-v-a", ""),
		})
		wantProblem(t, rep, "duplicate binding")
	})
	t.Run("duplicate gap flagged", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/gaps/x.textproto": "requirement_id: \"REQ-v-a\"\nreason: \"r\"\nlands { exists: \"REQ-v-b\" }\n",
			".stipulator/gaps/y.textproto": "requirement_id: \"REQ-v-a\"\nreason: \"stale twin\"\nlands { exists: \"REQ-v-b\" }\n",
		})
		wantProblem(t, rep, "gap for REQ-v-a duplicates")
	})
	t.Run("gap without id gets dedicated message", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/gaps/x.textproto": "reason: \"r\"\nlands { exists: \"REQ-v-a\" }\n",
		})
		wantProblem(t, rep, "gap without requirement_id")
	})
	t.Run("stray file in record dir is an error", func(t *testing.T) {
		fsys := fstest.MapFS{
			".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
			"specs/a.md":                     {Data: []byte(goodDoc)},
			".stipulator/bindings/README.md": {Data: []byte("stray")},
		}
		if _, err := records.Load(fsys); err == nil {
			t.Fatal("stray file tolerated")
		}
	})
}

// fakeBackend resolves from a fixed map: absent means NotFound; shape
// "GEN" means GeneratedFile.
type fakeBackend map[string]string

func (f fakeBackend) Resolve(symbol string) (Resolution, string, error) {
	shape, ok := f[symbol]
	switch {
	case !ok:
		return NotFound, "", nil
	case shape == "GEN":
		return GeneratedFile, "", nil
	}
	return Resolved, shape, nil
}

//gofresh:pure
func TestBackendResolution(t *testing.T) {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(goodDoc)},
		".stipulator/bindings/x.textproto": {Data: []byte(
			binding("REQ-v-a", "") + // symbol example.com/p.F
				strings.ReplaceAll(binding("REQ-v-b", ""), "example.com/p.F", "example.com/p.Gone") +
				strings.ReplaceAll(binding("REQ-v-a", ""), "example.com/p.F", "example.com/p.Generated") +
				strings.ReplaceAll(binding("REQ-v-b", ""), `backend: "go"`, `backend: "proto"`))},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	backends := map[string]Backend{"go": fakeBackend{
		"example.com/p.F":         strings.Repeat("s", 64),
		"example.com/p.Generated": "GEN",
	}}
	rep := Run(spec, store, backends, nil)
	wantProblem(t, rep, "generated file; bind the generating artifact")
	// A missing symbol is broken-bucket data, never a Problem: gap records
	// must be able to excuse it at the gate.
	for _, p := range rep.Problems {
		if strings.Contains(p.Message, "p.Gone") {
			t.Fatalf("NotFound reported as Problem: %v", p)
		}
	}
	if rep.Broken != 1 {
		t.Fatalf("broken = %d", rep.Broken)
	}
	var gone *BindingResult
	for i := range rep.Results {
		if rep.Results[i].Symbol == "example.com/p.Gone" {
			gone = &rep.Results[i]
		}
	}
	if gone == nil || gone.Resolution != NotFound {
		t.Fatalf("missing NotFound result: %+v", gone)
	}
	if rep.ShapeUnpinned != 1 { // p.F resolved, shape pin unset
		t.Fatalf("shape unpinned = %d", rep.ShapeUnpinned)
	}
	if rep.Unverified != 1 { // the proto-backend binding
		t.Fatalf("unverified = %d", rep.Unverified)
	}

	// Pin the shape, re-run: shape pinned.
	updates, err := records.Pin(store, nil, map[string]string{
		records.ShapeKey("go", "example.com/p.F"): strings.Repeat("s", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d", len(updates))
	}
	store2, err := records.Load(fstest.MapFS{
		".stipulator/bindings/x.textproto": {Data: updates[".stipulator/bindings/x.textproto"]},
	})
	if err != nil {
		t.Fatal(err)
	}
	rep2 := Run(spec, store2, backends, nil)
	if rep2.ShapePinned != 1 || rep2.ShapeUnpinned != 0 {
		t.Fatalf("after pin: shape pinned=%d unpinned=%d", rep2.ShapePinned, rep2.ShapeUnpinned)
	}
}

//gofresh:pure
func TestShapeMismatchIsDataNotProblem(t *testing.T) {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(goodDoc)},
		".stipulator/bindings/x.textproto": {Data: []byte(
			strings.Replace(binding("REQ-v-a", ""), "role:",
				"shape_hash: \""+strings.Repeat("0", 64)+"\"\n  role:", 1))},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	rep := Run(spec, store, map[string]Backend{"go": fakeBackend{
		"example.com/p.F": strings.Repeat("s", 64),
	}}, nil)
	if len(rep.Problems) != 0 {
		t.Fatalf("problems = %v", rep.Problems)
	}
	if rep.ShapeMismatch != 1 {
		t.Fatalf("shape mismatch = %d", rep.ShapeMismatch)
	}
}

//gofresh:pure
func TestWitnessCorrelation(t *testing.T) {
	testsBinding := strings.ReplaceAll(
		strings.ReplaceAll(binding("REQ-v-a", ""), "example.com/p.F", "example.com/p.TestA"),
		"BINDING_ROLE_IMPLEMENTS", "BINDING_ROLE_TESTS")
	failBinding := strings.ReplaceAll(
		strings.ReplaceAll(binding("REQ-v-b", ""), "example.com/p.F", "example.com/p.TestB"),
		"BINDING_ROLE_IMPLEMENTS", "BINDING_ROLE_TESTS")
	shadowBinding := strings.ReplaceAll(
		strings.ReplaceAll(binding("REQ-v-b", ""), "example.com/p.F", "example.com/p.TestC"),
		"BINDING_ROLE_IMPLEMENTS", "BINDING_ROLE_TESTS")
	provesBinding := strings.ReplaceAll(
		strings.ReplaceAll(binding("REQ-v-b", ""), "example.com/p.F", "example.com/p.TestD"),
		"BINDING_ROLE_IMPLEMENTS", "BINDING_ROLE_PROVES")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                       {Data: []byte(goodDoc)},
		".stipulator/bindings/x.textproto": {Data: []byte(testsBinding + failBinding + shadowBinding + provesBinding)},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	tr := &TestRun{
		RaceEnabled: true,
		Outcomes: map[string]TestOutcome{
			"example.com/p.TestA":     TestPassed,
			"example.com/p.TestA/sub": TestPassed,
			"example.com/p.TestB":     TestFailed,
			"example.com/p.TestD":     TestPassed,
		},
		Registrations: []Registration{
			{Package: "example.com/p", Test: "TestA/sub", Requirement: "REQ-v-a"}, // backed, subtest
			{Package: "example.com/p", Test: "TestA", Requirement: "REQ-v-b"},     // NOT backed by TestA
			{Package: "example.com/p", Test: "TestD", Requirement: "REQ-v-b"},     // backed by a proves-role binding
		},
		OutsidePolicy:   2,
		PackageFailures: map[string]string{"example.com/p": "package abort"},
	}
	rep := Run(spec, store, nil, tr)
	if rep.TestsPassed != 2 || rep.TestsFailed != 1 {
		t.Fatalf("tests passed=%d failed=%d", rep.TestsPassed, rep.TestsFailed)
	}
	// The witnessed run's visibility facts ride the report: the
	// outside-policy count and the package-keyed diagnostics reach every
	// report surface, never stop at the test run.
	if rep.OutsidePolicy != 2 || rep.PackageFailures["example.com/p"] != "package abort" {
		t.Fatalf("report lost witnessing facts: outside=%d failures=%v", rep.OutsidePolicy, rep.PackageFailures)
	}
	if rep.TestsNotRun != 1 { // TestC bound but produced no outcome
		t.Fatalf("tests not-run = %d (unwitnessed bound test must surface)", rep.TestsNotRun)
	}
	wantProblem(t, rep, "covers REQ-v-b, but no tests- or proves-role binding backs it")
	if len(rep.Registrations) != 2 || rep.Registrations[0].Requirement != "REQ-v-a" ||
		rep.Registrations[0].Outcome != TestPassed {
		t.Fatalf("registrations = %+v", rep.Registrations)
	}
	if rep.Registrations[1].Requirement != "REQ-v-b" || rep.Registrations[1].Outcome != TestPassed {
		t.Fatalf("proves-backed registration = %+v", rep.Registrations[1])
	}
	for _, r := range rep.Results {
		if r.Symbol == "example.com/p.TestB" && r.TestOutcome != TestFailed {
			t.Fatalf("failed test outcome = %v", r.TestOutcome)
		}
		// RaceEnabled qualifies a witness: only a passing outcome carries
		// the run's rigor claim — a failed or unwitnessed row has no
		// witness to qualify, and its producing rigor may differ from the
		// run-level attribute.
		if wantRace := r.TestOutcome == TestPassed && witnessRole(r.Role); r.RaceEnabled != wantRace {
			t.Fatalf("%s (outcome %v) race_enabled = %v, want %v", r.Symbol, r.TestOutcome, r.RaceEnabled, wantRace)
		}
	}
}

// errFS wraps an fs.FS, failing reads of one path with a non-NotExist error.
type errFS struct {
	fs.FS
	fail string
}

func (e errFS) ReadFile(name string) ([]byte, error) {
	if name == e.fail {
		return nil, fs.ErrPermission
	}
	return fs.ReadFile(e.FS, name)
}

//gofresh:pure
func TestUnreadableTombstonesPropagates(t *testing.T) {
	base := fstest.MapFS{
		".stipulator/tombstones.textproto": {Data: []byte("retired: \"REQ-old\"\n")},
	}
	if _, err := records.LoadTombstones(errFS{FS: base, fail: records.TombstonesPath}); err == nil {
		t.Fatal("permission error read as empty registry")
	}
	if _, err := records.LoadTombstones(fstest.MapFS{}); err != nil {
		t.Fatalf("absent registry must be empty, got %v", err)
	}
}

// TestSelfVerify checks this repository's own records against its own
// corpus: no dangling identities, no malformed records.
//
//gofresh:pure
func TestSelfVerify(t *testing.T) {
	fsys := os.DirFS("../..")
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	rep := Run(spec, store, nil, nil)
	for _, p := range rep.Problems {
		t.Error(p)
	}
	if len(store.Bindings) == 0 {
		t.Fatal("no binding files loaded")
	}
}

// TestAttestationRecordHygiene pins the contradictory-records refusal and
// the one-judgment rule: a requirement cannot be both gapped and
// attested, and duplicate attestations across files are problems.
//
//gofresh:pure
func TestAttestationRecordHygiene(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-attestation")
	rep, _ := run(t, map[string]string{
		".stipulator/gaps/a.textproto":          "requirement_id: \"REQ-v-a\"\nreason: \"deferred\"\nlands { manual { condition: \"x\" } }\n",
		".stipulator/attestations/a.textproto":  "attestations {\n  requirement_id: \"REQ-v-a\"\n  reason: \"judged fine\"\n}\n",
		".stipulator/attestations/b.textproto":  "attestations {\n  requirement_id: \"REQ-v-b\"\n  reason: \"first\"\n}\n",
		".stipulator/attestations/b2.textproto": "attestations {\n  requirement_id: \"REQ-v-b\"\n  reason: \"second\"\n}\n",
	})
	wantProblem(t, rep, "both gapped and attested; the records contradict — retract one: stipulator gap --req REQ-v-a --retract, or stipulator attest requirement --req REQ-v-a --retract")
	wantProblem(t, rep, "duplicates")
	// The contradicted and duplicated records yield no results beyond the
	// first judgment.
	if len(rep.Attestations) != 1 || rep.Attestations[0].RequirementId != "REQ-v-b" {
		t.Fatalf("attestation results = %+v", rep.Attestations)
	}
}

// TestWireWitnessClassMirrorsClassifier pins one-classifier-both-surfaces:
// the wire report carries the same evidence class the classifier resolved
// — for every class the classifier can produce and for both witness roles
// — so a per-binding view can never disagree with the per-requirement
// verdict. The map exhaustiveness arm is the tripwire for a class added
// to the enum but not the wire mirror (analyzer proofs shipped as
// UNSPECIFIED exactly that way).
//
//gofresh:pure
func TestWireWitnessClassMirrorsClassifier(t *testing.T) {
	stipulate.Covers(t, "REQ-report-messages")
	for wc := ExampleWitness; wc <= AnalyzerProof; wc++ {
		if _, ok := classProto[wc]; !ok {
			t.Errorf("witness class %d missing from the wire mirror", wc)
		}
	}
	for sl := Rearchitecture; sl <= SemanticDrift; sl++ {
		if _, ok := labelProto[sl]; !ok {
			t.Errorf("signature label %d missing from the wire mirror", sl)
		}
	}
	for _, role := range []stipulatorv1.BindingRole{
		stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
		stipulatorv1.BindingRole_BINDING_ROLE_PROVES,
	} {
		r := &Report{Results: []BindingResult{{
			RequirementId: "REQ-v-a", Symbol: "example.com/p.TestX", Backend: "go",
			Role: role, WitnessClass: AnalyzerProof, TestOutcome: TestPassed,
		}}}
		got := r.Proto().GetResults()[0].GetWitnessClass()
		if got != stipulatorv1.WitnessClass_WITNESS_CLASS_ANALYZER_PROOF {
			t.Errorf("role %v: wire class = %v, want ANALYZER_PROOF", role, got)
		}
	}
}

type sigBackend struct {
	shapes  map[string]string
	classes map[string]WitnessClass
}

func (b sigBackend) Resolve(sym string) (Resolution, string, error) {
	return Resolved, b.shapes[sym], nil
}
func (b sigBackend) WitnessClass(sym string) WitnessClass { return b.classes[sym] }

// TestChangeSignatures pins the classifier: pins are the baseline. A
// behavior witness failing under a current content pin is semantic
// drift; a proof-shape move (or proof failure) with every behavior
// witness green is a rearchitecture; a red witness whose contract text
// also moved carries a spec delta and is neither.
//
//gofresh:pure
func TestChangeSignatures(t *testing.T) {
	stipulate.Covers(t, "REQ-gate-change-signature")
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(goodDoc)},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	hashes := map[string]string{}
	for _, r := range spec.GetRequirements() {
		hashes[r.GetId()] = r.GetContentHash()
	}

	binding := func(req, sym, role, content, shape string) string {
		b := "bindings {\n  requirement_id: \"" + req + "\"\n  backend: \"go\"\n  symbol: \"" + sym + "\"\n  role: " + role + "\n"
		if content != "" {
			b += "  content_hash: \"" + content + "\"\n"
		}
		if shape != "" {
			b += "  shape_hash: \"" + shape + "\"\n"
		}
		return b + "}\n"
	}
	fsys[".stipulator/bindings/sig.textproto"] = &fstest.MapFile{Data: []byte(
		binding("REQ-v-a", "example.com/p.TestBehaviorA", "BINDING_ROLE_TESTS", hashes["REQ-v-a"], "s1") +
			binding("REQ-v-b", "example.com/p.TestProofB", "BINDING_ROLE_PROVES", hashes["REQ-v-b"], "old-shape") +
			binding("REQ-v-b", "example.com/p.TestBehaviorB", "BINDING_ROLE_TESTS", hashes["REQ-v-b"], "s2"))}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	backend := sigBackend{
		shapes: map[string]string{
			"example.com/p.TestBehaviorA": "s1",
			"example.com/p.TestProofB":    "new-shape", // moved against the pin
			"example.com/p.TestBehaviorB": "s2",
		},
		classes: map[string]WitnessClass{
			"example.com/p.TestBehaviorA": ExampleWitness,
			"example.com/p.TestProofB":    AnalyzerProof,
			"example.com/p.TestBehaviorB": ExampleWitness,
		},
	}
	tr := &TestRun{Outcomes: map[string]TestOutcome{
		"example.com/p.TestBehaviorA": TestFailed,
		"example.com/p.TestProofB":    TestPassed,
		"example.com/p.TestBehaviorB": TestPassed,
	}, RaceEnabled: true}
	rep := Run(spec, store, map[string]Backend{"go": backend}, tr)

	if len(rep.Signatures) != 2 {
		t.Fatalf("signatures = %+v", rep.Signatures)
	}
	drift, rearch := rep.Signatures[0], rep.Signatures[1]
	if drift.RequirementId != "REQ-v-a" || drift.Label != SemanticDrift ||
		!strings.Contains(strings.Join(drift.Evidence, ";"), "TestBehaviorA") {
		t.Fatalf("drift = %+v", drift)
	}
	if rearch.RequirementId != "REQ-v-b" || rearch.Label != Rearchitecture ||
		!strings.Contains(strings.Join(rearch.Evidence, ";"), "proof shape moved: example.com/p.TestProofB") {
		t.Fatalf("rearchitecture = %+v", rearch)
	}
	// The remediation floor: the moved shape names its re-pin in the
	// finding itself (REQ-change-remediation).
	if !strings.Contains(strings.Join(rearch.Evidence, ";"), "stipulator pin") {
		t.Fatalf("rearchitecture evidence withholds the computed remediation: %v", rearch.Evidence)
	}

	// A red witness whose contract text ALSO moved carries a spec delta:
	// no drift signature.
	fsys[".stipulator/bindings/sig.textproto"] = &fstest.MapFile{Data: []byte(
		binding("REQ-v-a", "example.com/p.TestBehaviorA", "BINDING_ROLE_TESTS", strings.Repeat("0", 64), "s1"))}
	store2, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	rep2 := Run(spec, store2, map[string]Backend{"go": backend}, tr)
	if len(rep2.Signatures) != 0 {
		t.Fatalf("stale-contract red produced a signature: %+v", rep2.Signatures)
	}

	// Proof moved AND behavior red with a stale contract: the red
	// carries a spec delta (no drift), and a red behavior witness
	// forbids the rearchitecture label — neither fires.
	fsys[".stipulator/bindings/sig.textproto"] = &fstest.MapFile{Data: []byte(
		binding("REQ-v-b", "example.com/p.TestProofB", "BINDING_ROLE_PROVES", hashes["REQ-v-b"], "old-shape") +
			binding("REQ-v-b", "example.com/p.TestBehaviorB", "BINDING_ROLE_TESTS", strings.Repeat("0", 64), "s2"))}
	storeMixed, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	trRed := &TestRun{Outcomes: map[string]TestOutcome{
		"example.com/p.TestProofB":    TestPassed,
		"example.com/p.TestBehaviorB": TestFailed,
	}, RaceEnabled: true}
	repMixed := Run(spec, storeMixed, map[string]Backend{"go": backend}, trRed)
	if len(repMixed.Signatures) != 0 {
		t.Fatalf("red-behavior rearchitecture mislabeled: %+v", repMixed.Signatures)
	}

	// An unwitnessed run derives no signatures: without outcomes there is
	// nothing to classify.
	rep3 := Run(spec, store, map[string]Backend{"go": backend}, nil)
	if len(rep3.Signatures) != 0 {
		t.Fatalf("unwitnessed run classified: %+v", rep3.Signatures)
	}
}
