package verify

import (
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
		"stipulator.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
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
	return Run(spec, store), store
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
	updates := records.Pin(store, hashes)
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
	// Deterministic: pinning twice produces identical bytes.
	store2, _ := records.Load(fstest.MapFS{
		".stipulator/bindings/x.textproto": {Data: got},
	})
	if again := records.Pin(store2, hashes); len(again) != 0 {
		t.Fatalf("re-pin of pinned file produced changes: %v", again)
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
	rep := Run(spec, store)
	for _, p := range rep.Problems {
		t.Error(p)
	}
	if len(store.Bindings) == 0 {
		t.Fatal("no binding files loaded")
	}
}
