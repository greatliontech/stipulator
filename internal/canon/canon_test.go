package canon

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/greatliontech/stipulator/stipulate"
)

func TestText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain text unchanged", "abc", "abc"},
		{"inner runs collapse", "a  b\t\nc", "a b c"},
		{"leading and trailing trimmed", "  a b \n", "a b"},
		{"unicode spaces collapse", "a  b", "a b"},
		{"decomposed composes to NFC", "café", "café"},
		{"empty stays empty", "", ""},
		{"whitespace-only becomes empty", " \n\t ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in); got != c.want {
				t.Fatalf("Text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestHash(t *testing.T) {
	stipulate.Covers(t, "REQ-model-hash-func")
	// Known SHA-256 vector: canonical form of "abc" is "abc" itself, so the
	// output must be the standard digest, pinning both the algorithm and
	// the lowercase-hex rendering.
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := Hash("abc"); got != abc {
		t.Fatalf("Hash(abc) = %q, want %q", got, abc)
	}
	if len(Hash("")) != 64 {
		t.Fatalf("hash length = %d, want 64", len(Hash("")))
	}

	// Normalization-insensitive equalities: formatting variants of the same
	// canonical text hash identically.
	equal := [][2]string{
		{"a  b\n c", "a b c"},
		{"café x", "café x"},
		{"  x  ", "x"},
	}
	for _, p := range equal {
		if Hash(p[0]) != Hash(p[1]) {
			t.Errorf("Hash(%q) != Hash(%q)", p[0], p[1])
		}
	}

	// Distinct canonical texts hash differently.
	if Hash("a b") == Hash("ab") {
		t.Error("whitespace collapse must not delete word boundaries")
	}
}

func FuzzTextProjection(f *testing.F) {
	for _, seed := range []string{
		"", "abc", "a  b", " x ", "café", "a b",
		"MUST not\tcollapse words", " line sep ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			t.Skip()
		}
		once := Text(s)
		if twice := Text(once); twice != once {
			t.Fatalf("not idempotent: Text(%q) = %q, Text again = %q", s, once, twice)
		}
		if strings.Contains(once, "  ") || once != strings.TrimSpace(once) {
			t.Fatalf("not collapsed/trimmed: %q", once)
		}
		if Hash(s) != Hash(once) {
			t.Fatalf("hash not stable under canonicalization of %q", s)
		}
	})
}
