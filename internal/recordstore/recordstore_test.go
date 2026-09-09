package recordstore

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/stipulator/stipulate"
	"pgregory.net/rapid"
)

// Every install batch leaves each touched identity holding exactly its
// bound of most recent variants — the batch's entries latest first
// ahead of the store's prior files — and untouched identities as they
// were, over random batch sequences, bounds, and identity overlaps; a
// model of the retention rule judges the store after every batch
// (REQ-evidence-record-store-layout).
//
//gofresh:pure
func TestInstallKeepsTheNewestVariantsPerIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	rapid.Check(t, func(rt *rapid.T) {
		store := Store{path: filepath.Join(t.TempDir(), "s")}
		bound := rapid.IntRange(1, 4).Draw(rt, "bound")
		var stamp int64
		// model holds, per identity, the retained names newest first.
		model := map[string][]string{}
		for round := rapid.IntRange(1, 6).Draw(rt, "rounds"); round > 0; round-- {
			n := rapid.IntRange(1, 5).Draw(rt, "batch")
			var batch []Entry
			for i := 0; i < n; i++ {
				identity := rapid.SampledFrom([]string{"a", "b", "c"}).Draw(rt, "identity")
				name := Name([]string{identity}, rapid.IntRange(0, 99).Draw(rt, "fp"))
				batch = append(batch, Entry{Name: name, Data: []byte(name)})
			}
			if err := store.Install(bound, batch...); err != nil {
				rt.Fatal(err)
			}
			// Distinct install times in batch order, later than every
			// earlier file: the store's recency order is then total.
			for _, e := range batch {
				stamp++
				at := time.Unix(0, stamp*int64(time.Second))
				os.Chtimes(filepath.Join(store.path, e.Name), at, at)
				identity := identityOf(e.Name)
				retained := []string{e.Name}
				for _, prior := range model[identity] {
					if prior != e.Name {
						retained = append(retained, prior)
					}
				}
				model[identity] = retained
			}
			for identity, retained := range model {
				if len(retained) > bound {
					model[identity] = retained[:bound]
				}
			}
			names, err := store.Names()
			if err != nil {
				rt.Fatal(err)
			}
			got := map[string][]string{}
			for _, name := range names {
				if data, err := store.Read(name); err != nil || string(data) != name+"\n" {
					rt.Fatalf("%s carries %q, %v", name, data, err)
				}
				got[identityOf(name)] = append(got[identityOf(name)], name)
			}
			for identity, retained := range model {
				if !slices.Equal(got[identity], retained) {
					rt.Fatalf("identity %s holds %v, want %v", identity, got[identity], retained)
				}
			}
			if len(got) != len(model) {
				rt.Fatalf("identities %v, want %v", got, model)
			}
		}
	})
}

// Names lists newest first with names breaking ties, and never a
// directory, a dot-prefixed temporary, or a non-.json file; a missing
// store is the error, and an unreadable file is its read's error.
//
//gofresh:pure
func TestNamesOrderAndTemporaries(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	store := Store{kind: "k", path: t.TempDir()}
	write := func(name string, at time.Time) {
		full := filepath.Join(store.path, name)
		if err := os.WriteFile(full, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(full, at, at)
	}
	base := time.Unix(1_000_000, 0)
	write("b-1.json", base)
	write("a-1.json", base)
	write("c-1.json", base.Add(time.Second))
	write(".k-1.json", base.Add(time.Hour))
	write("notes.json.txt", base.Add(time.Hour))
	os.Mkdir(filepath.Join(store.path, "ledgers"), 0o755)
	names, err := store.Names()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c-1.json", "a-1.json", "b-1.json"}; !slices.Equal(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	if _, err := (Store{kind: "k", path: filepath.Join(store.path, "absent")}).Names(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing store: %v", err)
	}
	if os.Getuid() != 0 {
		os.Chmod(filepath.Join(store.path, "a-1.json"), 0)
		if data, err := store.Read("a-1.json"); err == nil {
			t.Fatalf("unreadable file read %q", data)
		}
	}
}

// An install bounds only the identities it touches: an identity the
// batch never names keeps every variant it holds, however many — the
// garbage-collection verb is the one eviction across identities
// (REQ-evidence-store-gc).
//
//gofresh:pure
func TestInstallNeverEvictsAnUntouchedIdentity(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	store := Store{kind: "k", path: t.TempDir()}
	for i := 0; i < 6; i++ {
		name := Name([]string{"crowded"}, i)
		if err := os.WriteFile(filepath.Join(store.path, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Install(2, Entry{Name: Name([]string{"other"}, 1), Data: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	names, _ := store.Names()
	crowded := 0
	for _, name := range names {
		if identityOf(name) == Digest("crowded") {
			crowded++
		}
	}
	if crowded != 6 || len(names) != 7 {
		t.Fatalf("store holds %v: the untouched identity was trimmed", names)
	}
}

// Sweep removes what keep refuses and counts both sides; a temporary is
// never swept; an unreadable file reaches keep without data; the first
// removal failure is the error and the walk continues; a store that
// cannot be listed is the error.
//
//gofresh:pure
func TestSweepCountsAndSparesTemporaries(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	store := Store{kind: "k", path: filepath.Join(t.TempDir(), "s")}
	if removed, kept, err := store.Sweep(func(string, []byte) bool { return false }); removed != 0 || kept != 0 || err != nil {
		t.Fatalf("missing store: %d %d %v", removed, kept, err)
	}
	if err := store.Install(4, Entry{"a-1.json", []byte("a")}, Entry{"a-2.json", []byte("b")}, Entry{"b-1.json", []byte("c")}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(store.path, ".k-live.json"), []byte("x"), 0o644)
	removed, kept, err := store.Sweep(func(name string, data []byte) bool { return strings.HasPrefix(name, "a-") && data != nil })
	if removed != 1 || kept != 2 || err != nil {
		t.Fatalf("sweep = %d removed, %d kept, %v", removed, kept, err)
	}
	if _, err := os.Stat(filepath.Join(store.path, ".k-live.json")); err != nil {
		t.Fatalf("the temporary was swept: %v", err)
	}
	if os.Getuid() != 0 {
		os.Chmod(filepath.Join(store.path, "a-1.json"), 0)
		var sawNil bool
		removed, kept, err = store.Sweep(func(name string, data []byte) bool { sawNil = sawNil || data == nil; return data != nil })
		os.Chmod(filepath.Join(store.path, "a-1.json"), 0o644)
		if !sawNil || removed != 1 || kept != 1 || err != nil {
			t.Fatalf("unreadable sweep = %d %d %v (nil seen %v)", removed, kept, err, sawNil)
		}
		os.Chmod(store.path, 0o555)
		defer os.Chmod(store.path, 0o755)
		removed, kept, err = store.Sweep(func(string, []byte) bool { return false })
		if removed != 0 || kept != 0 || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("read-only sweep = %d %d %v", removed, kept, err)
		}
	}
	file := Store{kind: "k", path: filepath.Join(t.TempDir(), "file")}
	os.WriteFile(file.path, []byte("x"), 0o644)
	if _, _, err := file.Sweep(func(string, []byte) bool { return true }); err == nil {
		t.Fatal("a store that is a file swept without error")
	}
}

// Name and Open follow the layout: identity digest, fingerprint digest
// over the canonical JSON, the corpus root's resolved path under the
// kind's root.
//
//gofresh:pure
func TestLayout(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-record-store-layout")
	name := Name([]string{"g", "p", "T"}, map[string]string{"k": "v"})
	if !strings.HasSuffix(name, ".json") || len(name) != 16+1+16+5 || name[:16] != Digest("g", "p", "T") {
		t.Fatalf("name = %q", name)
	}
	if Name([]string{"g"}, 1) == Name([]string{"g"}, 2) || Name([]string{"g"}, 1) != Name([]string{"g"}, 1) {
		t.Fatal("the fingerprint segment does not follow the fingerprint")
	}
	if Digest("a", "bc") == Digest("ab", "c") {
		t.Fatal("identity parts are not separated")
	}
	if identityOf("nodash.json") != "" {
		t.Fatal("a name without an identity segment has one")
	}
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip(err)
	}
	a, err := Open("kind", dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open("kind", link)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := Root("kind")
	if a.Path() != b.Path() || filepath.Dir(a.Path()) != root || !strings.HasSuffix(root, filepath.Join("stipulator", "kind")) {
		t.Fatalf("paths %q %q under %q", a.Path(), b.Path(), root)
	}
	if !strings.HasPrefix(a.pattern(), ".") || !strings.HasSuffix(a.pattern(), ".json") || !strings.Contains(a.pattern(), "kind") {
		t.Fatalf("install temporary pattern %q is not dot-prefixed", a.pattern())
	}
}
