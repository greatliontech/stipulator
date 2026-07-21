package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
	"github.com/greatliontech/stipulator/stipulate"
)

// applyUpdates is a compare-and-swap batch: a target that moved since
// the operation read it refuses the WHOLE batch before the first write,
// so a concurrent agent's records are never silently dropped
// (REQ-record-cas).
//
//gofresh:pure
func TestApplyUpdatesCompareAndSwap(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return ""
		}
		return string(b)
	}
	write(".stipulator/gaps/a.textproto", "original a")
	write(".stipulator/gaps/b.textproto", "original b")

	// Happy path: matching priors apply; a new file lands read-absent.
	if err := applyUpdates(dir, []author.Update{
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("new a"), Prior: []byte("original a")},
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("fresh"), PriorAbsent: true},
	}); err != nil {
		t.Fatal(err)
	}
	if read(".stipulator/gaps/a.textproto") != "new a" || read(".stipulator/gaps/new.textproto") != "fresh" {
		t.Fatal("matching priors did not apply")
	}

	// A moved target refuses the whole batch: the FIRST update's clean
	// write must not land when the second's precondition fails.
	err := applyUpdates(dir, []author.Update{
		{Path: ".stipulator/gaps/new.textproto", Content: []byte("second write"), Prior: []byte("fresh")},
		{Path: ".stipulator/gaps/b.textproto", Content: nil, Prior: []byte("what the operation thought it read")},
	})
	if err == nil {
		t.Fatal("moved target accepted")
	}
	if read(".stipulator/gaps/new.textproto") != "fresh" {
		t.Fatal("batch partially applied despite a failed precondition")
	}
	if read(".stipulator/gaps/b.textproto") != "original b" {
		t.Fatal("deletion landed despite the failed precondition")
	}

	// Read-absent vs appeared: a file that appeared refuses.
	if err := applyUpdates(dir, []author.Update{
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("x"), PriorAbsent: true},
	}); err == nil {
		t.Fatal("appeared file accepted under a read-absent precondition")
	}

	// Vanished: a deletion whose target is gone refuses.
	if err := applyUpdates(dir, []author.Update{
		{Path: ".stipulator/gaps/ghost.textproto", Content: nil, Prior: []byte("was there")},
	}); err == nil {
		t.Fatal("vanished target accepted")
	}

	// An unstamped update (neither prior nor read-absence) is a
	// programming error refused loudly — the class that escaped once.
	if err := applyUpdates(dir, []author.Update{
		{Path: ".stipulator/gaps/unstamped.textproto", Content: []byte("x")},
	}); err == nil {
		t.Fatal("unstamped update accepted")
	}

	// A batch naming one path twice is ambiguous and refused.
	if err := applyUpdates(dir, []author.Update{
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("p"), Prior: []byte("new a")},
		{Path: ".stipulator/gaps/a.textproto", Content: []byte("q"), Prior: []byte("new a")},
	}); err == nil {
		t.Fatal("duplicate-path batch accepted")
	}
}

// The verbs stamp what they read: an update over an existing record
// carries its bytes, a fresh record stamps read-absent — the
// preconditions applyUpdates enforces (REQ-record-cas).
//
//gofresh:pure
func TestVerbsStampPriors(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".stipulator/manifest.textproto", "include: \"specs/**/*.md\"\n")
	write("specs/s.md", "# S\n\n**REQ-cas-a** (behavior): It MUST a.\n\n**REQ-cas-b** (behavior): It MUST b.\n")
	existing := "requirement_id: \"REQ-cas-a\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n"
	write(".stipulator/gaps/cas-a.textproto", existing)

	fsys := os.DirFS(dir)
	ups, err := author.RetractGaps(fsys, []string{"REQ-cas-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || string(ups[0].Prior) != existing || ups[0].PriorAbsent {
		t.Fatalf("retraction prior = %q absent=%v, want the record's bytes", ups[0].Prior, ups[0].PriorAbsent)
	}

	lc, err := author.NewLandingCondition("", "", "later", false)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := author.Gaps(fsys, []string{"REQ-cas-b"}, "new", lc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || !fresh[0].PriorAbsent {
		t.Fatalf("fresh gap not stamped read-absent: %+v", fresh[0])
	}

	// The escaped class: a bind into an EXISTING binding file must stamp
	// that file's bytes — the unstamped zero value reads as
	// "expected empty" and refuses every second write to the same file.
	existingBinding := "bindings {\n" +
		"  requirement_id: \"REQ-cas-a\"\n" +
		"  backend: \"go\"\n" +
		"  symbol: \"example.com/x.F\"\n" +
		"  role: BINDING_ROLE_IMPLEMENTS\n" +
		"}\n"
	write(".stipulator/bindings/cas.textproto", existingBinding)
	up, err := author.Bind(fsys, nil, author.BindRequest{
		Requirement: "REQ-cas-b", Symbol: "example.com/x.G", Backend: "go",
		Role: stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS, File: ".stipulator/bindings/cas.textproto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.PriorAbsent || string(up.Prior) != existingBinding {
		t.Fatalf("bind over an existing file stamped prior=%q absent=%v", up.Prior, up.PriorAbsent)
	}
}

// For every batch over every tree: the apply lands exactly when every
// target still matches what the operation read — a single moved,
// appeared, or vanished target refuses the WHOLE batch and leaves every
// target byte-identical (REQ-record-cas, quantified).
//
//gofresh:pure
func TestPropApplyRefusesIffAnyTargetMoved(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		nFiles := rapid.IntRange(1, 5).Draw(rt, "nFiles")
		content := func(i, gen int) []byte {
			return []byte(fmt.Sprintf("record %d gen %d", i, gen))
		}
		pathOf := func(i int) string { return fmt.Sprintf(".stipulator/gaps/f%d.textproto", i) }
		fullOf := func(i int) string { return filepath.Join(dir, filepath.FromSlash(pathOf(i))) }
		exists := make([]bool, nFiles)
		if err := os.MkdirAll(filepath.Join(dir, ".stipulator/gaps"), 0o755); err != nil {
			rt.Fatal(err)
		}
		for i := 0; i < nFiles; i++ {
			exists[i] = rapid.Bool().Draw(rt, fmt.Sprintf("exists%d", i))
			if exists[i] {
				if err := os.WriteFile(fullOf(i), content(i, 0), 0o644); err != nil {
					rt.Fatal(err)
				}
			}
		}
		// The batch: each chosen target's update, stamped from the state
		// just written — exactly what a verb's load would have read.
		var ups []author.Update
		for i := 0; i < nFiles; i++ {
			if !rapid.Bool().Draw(rt, fmt.Sprintf("in%d", i)) {
				continue
			}
			up := author.Update{Path: pathOf(i)}
			if exists[i] {
				up.Prior = content(i, 0)
			} else {
				up.PriorAbsent = true
			}
			if exists[i] && rapid.Bool().Draw(rt, fmt.Sprintf("del%d", i)) {
				up.Content = nil
			} else {
				up.Content = content(i, 1)
			}
			ups = append(ups, up)
		}
		if len(ups) == 0 {
			return
		}
		// The concurrent writer: maybe move one target after the stamp.
		moved := -1
		if rapid.Bool().Draw(rt, "interfere") {
			victim := ups[rapid.IntRange(0, len(ups)-1).Draw(rt, "victim")]
			idx := -1
			for i := 0; i < nFiles; i++ {
				if pathOf(i) == victim.Path {
					idx = i
				}
			}
			switch {
			case exists[idx]:
				if rapid.Bool().Draw(rt, "vanish") {
					if err := os.Remove(fullOf(idx)); err != nil {
						rt.Fatal(err)
					}
				} else if err := os.WriteFile(fullOf(idx), []byte("concurrent"), 0o644); err != nil {
					rt.Fatal(err)
				}
			default:
				if err := os.WriteFile(fullOf(idx), []byte("appeared"), 0o644); err != nil {
					rt.Fatal(err)
				}
			}
			moved = idx
		}
		before := map[string][]byte{}
		for i := 0; i < nFiles; i++ {
			if b, err := os.ReadFile(fullOf(i)); err == nil {
				before[pathOf(i)] = b
			}
		}
		err := applyUpdates(dir, ups)
		if moved >= 0 {
			if err == nil {
				rt.Fatalf("a moved target (%s) did not refuse the batch", pathOf(moved))
			}
			// All-or-nothing: every target byte-identical to pre-apply.
			for i := 0; i < nFiles; i++ {
				after, aerr := os.ReadFile(fullOf(i))
				prior, had := before[pathOf(i)]
				if (aerr == nil) != had || (had && string(after) != string(prior)) {
					rt.Fatalf("refused batch mutated %s", pathOf(i))
				}
			}
			return
		}
		if err != nil {
			rt.Fatalf("clean batch refused: %v", err)
		}
		for _, up := range ups {
			after, aerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(up.Path)))
			if up.Content == nil {
				if aerr == nil {
					rt.Fatalf("deletion did not land for %s", up.Path)
				}
				continue
			}
			if aerr != nil || string(after) != string(up.Content) {
				rt.Fatalf("write did not land for %s", up.Path)
			}
		}
	})
}
