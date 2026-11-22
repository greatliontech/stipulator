package resolutioncache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

func TestResolutionRecordsRequireTheWholeEnvelope(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	for _, suffix := range []string{"", " \t\r\n", "{}", "null", "[]", "true", "0", `"tail"`, "garbage", "\x00", "\v", "\xff", "\n{}\nnull"} {
		t.Run(fmt.Sprintf("%q", suffix), func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			dir := t.TempDir()
			good := Record{Selection: "default", Symbol: "example.com/p.Good", Views: views("default", "a"), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
			candidate := good.Clone()
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
			if !seen[good.Symbol].Equal(good) || valid && !seen[candidate.Symbol].Equal(candidate) {
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
		rec := Record{Selection: "default", Symbol: "example.com/p.F", Views: views("default", "a"), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
		data, err := json.Marshal(entry{Version: version, Selection: rec.Selection, Symbol: rec.Symbol, Views: rec.Views, Resolution: rec.Resolution, Shape: rec.Shape, Package: rec.Package, WitnessClass: rec.WitnessClass})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := decodeRecord(mustName(t, rec), append(data, suffix...))
		want := len(bytes.Trim(suffix, " \t\r\n")) == 0
		if ok != want || ok && !got.Equal(rec) {
			t.Fatalf("suffix %q: admitted=%t want=%t, record=%+v", suffix, ok, want, got)
		}
	})
}

func TestResolutionViewEntryRejectsUnsupportedMembers(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	presentEntry := ViewEntry{Selection: "default", Present: true, Fingerprint: fingerprint("a")}
	presentRaw, err := json.Marshal(presentEntry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ViewEntry
	if err := json.Unmarshal(presentRaw, &decoded); err != nil || decoded != presentEntry {
		t.Fatalf("valid present view entry: %+v %v", decoded, err)
	}
	absentEntry := ViewEntry{Selection: "race", Present: false}
	absentRaw, err := json.Marshal(absentEntry)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(absentRaw, &decoded); err != nil || decoded != absentEntry {
		t.Fatalf("valid absent view entry: %+v %v", decoded, err)
	}
	for name, raw := range map[string]string{
		"absent fields":         `{}`,
		"null":                  `null`,
		"missing present":       `{"selection":"default"}`,
		"null present":          `{"selection":"default","present":null}`,
		"empty selection":       `{"selection":"","present":false}`,
		"present without fp":    `{"selection":"default","present":true}`,
		"absent with fp":        strings.Replace(string(presentRaw), `"present":true`, `"present":false`, 1),
		"unknown field":         strings.TrimSuffix(string(presentRaw), "}") + `,"extra":0}`,
		"duplicate field":       strings.TrimSuffix(string(absentRaw), "}") + `,"present":false}`,
		"case alias":            strings.Replace(string(absentRaw), `"present"`, `"Present"`, 1),
		"null fingerprint":      `{"selection":"default","present":true,"fingerprint":null}`,
		"code-result fp":        strings.Replace(string(presentRaw), `"resultKind":3`, `"resultKind":1`, 1),
		"invalid closure bytes": strings.Replace(string(presentRaw), strings.Repeat("a", 32), strings.Repeat("z", 32), 1),
	} {
		t.Run(name, func(t *testing.T) {
			var got ViewEntry
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatalf("unsupported view entry admitted: %s", raw)
			}
		})
	}
}

func TestResolutionEnvelopeRejectsAmbiguousAnswers(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := Record{Selection: "default", Symbol: "example.com/p.F", Views: views("default", "a"), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	root, _ := StoreDir(dir)
	name := mustName(t, rec)
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := decodeRecord(name, data); !ok || !got.Equal(rec) {
		t.Fatalf("valid answer refused: %+v %t", got, ok)
	}
	for label, raw := range map[string]string{
		"duplicate verdict": strings.TrimSuffix(string(data), "}") + `,"resolution":"resolved"}`,
		"case alias":        strings.Replace(string(data), `"resolution"`, `"Resolution"`, 1),
		"null reason":       strings.TrimSuffix(string(data), "}") + `,"neverServe":null}`,
		"null views":        strings.Replace(string(data), `"views": [`, `"views": null, "extra": [`, 1),
		"null view element": strings.Replace(string(data), `"views": [`, `"views": [null,`, 1),
		"unknown field":     strings.TrimSuffix(string(data), "}") + `,"extra":0}`,
		"empty shape":       strings.Replace(string(data), `"shape": "shape"`, `"shape": ""`, 1),
		"wrong owner":       strings.Replace(string(data), `"package": "example.com/p"`, `"package": "example.com/other"`, 1),
		"unknown class":     strings.Replace(string(data), `"witnessClass": "example"`, `"witnessClass": "future"`, 1),
	} {
		t.Run(label, func(t *testing.T) {
			if raw == string(data) {
				t.Fatal("tamper did not change input")
			}
			if _, ok := decodeRecord(name, []byte(raw)); ok {
				t.Fatalf("ambiguous answer admitted: %s", raw)
			}
		})
	}
}

func TestResolutionObjectMembers(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	for _, tc := range []struct {
		data string
		want bool
	}{
		{`{"reason":""}`, true},
		{`{"reason":null}`, false},
		{`{"reason":"","reason":""}`, false},
		{`{"Reason":""}`, false},
		{`{"other":""}`, false},
		{`{"reason":""} null`, false},
	} {
		if _, ok := objectFields([]byte(tc.data), "reason"); ok != tc.want {
			t.Errorf("object %s: admitted=%t want=%t", tc.data, ok, tc.want)
		}
	}
}
