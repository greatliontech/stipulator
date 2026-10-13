package resolutioncache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

func TestResolutionRecordsRequireTheWholeEnvelope(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	for _, suffix := range []string{"", " \t\r\n", "{}", "null", "[]", "true", "0", `"tail"`, "garbage", "\x00", "\v", "\xff", "\n{}\nnull"} {
		t.Run(fmt.Sprintf("%q", suffix), func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			good := Record{Selection: "default", Symbol: "example.com/p.Good", Fingerprint: fingerprint("a"), Resolution: "resolved", Package: "example.com/p", WitnessClass: "example"}
			candidate := good
			candidate.Symbol = "example.com/p.Candidate"
			if err := InstallAll(dir, []Record{good, candidate}); err != nil {
				t.Fatal(err)
			}
			root, err := StoreDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, mustName(t, candidate))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data := append(append([]byte(nil), original...), suffix...)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			valid := suffix == "" || suffix == " \t\r\n"
			want := 1
			if valid {
				want++
			}
			loaded := Load(dir)
			if len(loaded) != want {
				t.Fatalf("loaded %d records, want %d; suffix=%q", len(loaded), want, suffix)
			}
			seen := map[string]Record{}
			for _, rec := range loaded {
				seen[rec.Symbol] = rec
			}
			if seen[good.Symbol] != good || valid && seen[candidate.Symbol] != candidate {
				t.Fatalf("record fields or independent sibling changed: %+v", loaded)
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, data) {
				t.Fatalf("inspection rewrote the input: %v", err)
			}
			calls := 0
			removed, kept, err := GC(t.Context(), dir, func(_, symbol string) bool {
				calls++
				if !valid && symbol == candidate.Symbol {
					t.Error("collection offered a malformed record to the liveness predicate")
				}
				return true
			})
			if err != nil || removed != 2-want || kept != want || calls != want {
				t.Fatalf("collection = removed %d, kept %d, callbacks %d, err %v", removed, kept, calls, err)
			}
			if !valid {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("malformed file survived collection: %v", err)
				}
			}
			if got := Load(dir); len(got) != want {
				t.Fatalf("collection lost a valid sibling: %+v", got)
			}
		})
	}
}

func FuzzResolutionEnvelopeSuffix(f *testing.F) {
	for _, suffix := range []string{"", " \t\r\n", "{}", "null", "[]", "true", "false", "0", `"tail"`, "\x00", "\v", "\xff", "\u00a0", "\n{}\nnull"} {
		f.Add([]byte(suffix))
	}
	f.Fuzz(func(t *testing.T, suffix []byte) {
		stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
		rec := Record{Selection: "default", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Resolution: "resolved"}
		data, err := json.Marshal(entry{Version: version, Selection: rec.Selection, Symbol: rec.Symbol, Fingerprint: rec.Fingerprint, Resolution: rec.Resolution})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := decodeRecord(mustName(t, rec), append(data, suffix...))
		want := len(bytes.Trim(suffix, " \t\r\n")) == 0
		if ok != want || ok && got != rec {
			t.Fatalf("suffix %q: admitted=%t want=%t, record=%+v", suffix, ok, want, got)
		}
	})
}
