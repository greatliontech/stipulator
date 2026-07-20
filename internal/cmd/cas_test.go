package cmd

import (
	"os"
	"path/filepath"
	"testing"

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
	fresh, _, err := author.Gaps(fsys, []string{"REQ-cas-b"}, "new", lc)
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
