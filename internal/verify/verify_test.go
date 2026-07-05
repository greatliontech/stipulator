package verify

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
)

const goodDoc = "# T\n\n**REQ-v-a** (behavior): It MUST x.\n\n**REQ-v-b** (behavior): It MUST y.\n"

func run(t *testing.T, files map[string]string) (*Report, *records.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":           {Data: []byte(goodDoc)},
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

func TestConsistency(t *testing.T) {
	t.Run("dangling binding", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/bindings/x.textproto": binding("REQ-v-ghost", ""),
		})
		wantProblem(t, rep, "names REQ-v-ghost, which is not in the corpus")
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

func TestRecordHygiene(t *testing.T) {
	t.Run("duplicate binding flagged", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/bindings/x.textproto": binding("REQ-v-a", "") + binding("REQ-v-a", ""),
		})
		wantProblem(t, rep, "duplicate binding")
	})
	t.Run("gap without id gets dedicated message", func(t *testing.T) {
		rep, _ := run(t, map[string]string{
			".stipulator/gaps/x.textproto": "reason: \"r\"\nlands { exists: \"REQ-v-a\" }\n",
		})
		wantProblem(t, rep, "gap without requirement_id")
	})
	t.Run("stray file in record dir is an error", func(t *testing.T) {
		fsys := fstest.MapFS{
			".stipulator/manifest.textproto":             {Data: []byte("include: \"specs/**/*.md\"\n")},
			"specs/a.md":                       {Data: []byte(goodDoc)},
			".stipulator/bindings/README.md":   {Data: []byte("stray")},
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

func TestBackendResolution(t *testing.T) {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":           {Data: []byte(goodDoc)},
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

func TestShapeMismatchIsDataNotProblem(t *testing.T) {
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":           {Data: []byte(goodDoc)},
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
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto":             {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                       {Data: []byte(goodDoc)},
		".stipulator/bindings/x.textproto": {Data: []byte(testsBinding + failBinding + shadowBinding)},
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
		Outcomes: map[string]TestOutcome{
			"example.com/p.TestA":     TestPassed,
			"example.com/p.TestA/sub": TestPassed,
			"example.com/p.TestB":     TestFailed,
		},
		Registrations: []Registration{
			{Package: "example.com/p", Test: "TestA/sub", Requirement: "REQ-v-a"}, // backed, subtest
			{Package: "example.com/p", Test: "TestA", Requirement: "REQ-v-b"},     // NOT backed by TestA
		},
	}
	rep := Run(spec, store, nil, tr)
	if rep.TestsPassed != 1 || rep.TestsFailed != 1 {
		t.Fatalf("tests passed=%d failed=%d", rep.TestsPassed, rep.TestsFailed)
	}
	if rep.TestsNotRun != 1 { // TestC bound but produced no outcome
		t.Fatalf("tests not-run = %d (unwitnessed bound test must surface)", rep.TestsNotRun)
	}
	wantProblem(t, rep, "covers REQ-v-b, but no role-tests binding backs it")
	if len(rep.Registrations) != 1 || rep.Registrations[0].Requirement != "REQ-v-a" ||
		rep.Registrations[0].Outcome != TestPassed {
		t.Fatalf("registrations = %+v", rep.Registrations)
	}
	for _, r := range rep.Results {
		if r.Symbol == "example.com/p.TestB" && r.TestOutcome != TestFailed {
			t.Fatalf("failed test outcome = %v", r.TestOutcome)
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
