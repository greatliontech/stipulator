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
			good := Record{Selection: "default", Symbol: "example.com/p.Good", Fingerprint: fingerprint("a"), Certificate: certificate(), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
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
		rec := Record{Selection: "default", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Certificate: certificate(), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
		data, err := json.Marshal(entry{Version: version, Selection: rec.Selection, Symbol: rec.Symbol, Fingerprint: rec.Fingerprint, Certificate: rec.Certificate, Resolution: rec.Resolution, Shape: rec.Shape, Package: rec.Package, WitnessClass: rec.WitnessClass})
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

func TestResolutionCertificateRejectsUnsupportedMembers(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	good, err := json.Marshal(certificate())
	if err != nil {
		t.Fatal(err)
	}
	var decoded Certificate
	if err := json.Unmarshal(good, &decoded); err != nil || decoded != certificate() {
		t.Fatalf("valid certificate: %+v %v", decoded, err)
	}
	for name, raw := range map[string]string{
		"absent fields":  `{}`,
		"null":           `null`,
		"future rule":    strings.Replace(string(good), Derivation, "future", 1),
		"invalid digest": strings.Replace(string(good), certificate().Inputs, strings.Repeat("z", 64), 1),
		"short digest":   strings.Replace(string(good), certificate().Inputs, "a", 1),
		"unknown":        strings.TrimSuffix(string(good), "}") + `,"extra":0}`,
		"duplicate":      strings.TrimSuffix(string(good), "}") + `,"derivation":"` + Derivation + `"}`,
		"case alias":     strings.Replace(string(good), `"inputs"`, `"Inputs"`, 1),
		"null inputs":    strings.Replace(string(good), `"`+certificate().Inputs+`"`, `null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var got Certificate
			if err := json.Unmarshal([]byte(raw), &got); err == nil {
				t.Fatalf("unsupported certificate admitted: %s", raw)
			}
		})
	}
}

func TestResolutionEnvelopeRejectsAmbiguousAnswers(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-cache-format")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	rec := Record{Selection: "default", Symbol: "example.com/p.F", Fingerprint: fingerprint("a"), Certificate: certificate(), Resolution: "resolved", Shape: "shape", Package: "example.com/p", WitnessClass: "example"}
	if err := InstallAll(dir, []Record{rec}); err != nil {
		t.Fatal(err)
	}
	root, _ := StoreDir(dir)
	name := mustName(t, rec)
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := decodeRecord(name, data); !ok || got != rec {
		t.Fatalf("valid answer refused: %+v %t", got, ok)
	}
	for label, raw := range map[string]string{
		"duplicate verdict": strings.TrimSuffix(string(data), "}") + `,"resolution":"resolved"}`,
		"case alias":        strings.Replace(string(data), `"resolution"`, `"Resolution"`, 1),
		"null reason":       strings.TrimSuffix(string(data), "}") + `,"neverServe":null}`,
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
