package canon

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/greatliontech/stipulator/stipulate"
	"golang.org/x/text/unicode/norm"
	"pgregory.net/rapid"
)

//gofresh:pure
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

//gofresh:pure
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

// Text's output alphabet carries no control characters — the property
// HashParts' block delimiter rests on: non-whitespace controls (C0, C1,
// DEL) are removed, whitespace controls collapse with all whitespace.
func TestTextStripsControlCharacters(t *testing.T) {
	cases := map[string]string{
		"a\x1eb":      "ab",
		"a\x00b\x7fc": "abc",
		"a\u0085b":    "a b", // NEL is Unicode whitespace: collapses, not stripped
		"a\tb\nc":     "a b c",
		"\x1e":        "",
		"a\u009cb":    "ab", // C1 control: stripped
	}
	for in, want := range cases {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
		for _, r := range Text(in) {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("Text(%q) output carries control %U", in, r)
			}
		}
	}

	// The strip precedes normalization: a control blocking canonical
	// composition must vanish as if never there, so the with-control
	// and without-control forms canonicalize identically and Text stays
	// a projection.
	if Text("a\u0001\u0301") != Text("a\u0301") {
		t.Errorf("control-equivalence broken: %q vs %q", Text("a\u0001\u0301"), Text("a\u0301"))
	}
	for _, in := range []string{"a\u0001\u0301", "a\x1eb", "a\u0085b", "e\u0301\x00\u0308"} {
		if Text(Text(in)) != Text(in) {
			t.Errorf("projection broken for %q: once=%q twice=%q", in, Text(in), Text(Text(in)))
		}
	}
}

// The three coupled canon contracts — projection, control-free output
// alphabet, delimiter unrepresentability (and HashParts' single-part
// equality) — pinned over a generated alphabet rather than hand-chosen
// examples: ASCII, C0/C1/DEL, format characters, whitespace variants,
// and combining marks in any combination.
//
//gofresh:pure
func TestPropCanonProjection(t *testing.T) {
	// Tokens rather than lone runes: the adversarial shapes (a control
	// BLOCKING canonical composition between a starter and a combining
	// mark) need adjacency, and independent rune draws hit a specific
	// triple too rarely to trip an ordering fault reliably — measured:
	// the strip-after-NFC mutant survived 100 lone-rune draws and dies
	// in one token draw.
	alphabet := []string{
		"a", "b", "e", "o", "u", "A", "Z", "9", ".", " ", "\t", "\n",
		"\x00", "\x01", "\x1e", "\x1f", "\x7f", "\u0085", "\u009c", "\u00a0",
		"\u200b", "\ufeff", "\u0301", "\u0308", "\u00e9", "\u1680", "\u3000",
		"a\x01\u0301", "e\u0301\x00\u0308", "\x1e\u0301", "Z\x7f\u0308",
	}
	rapid.Check(t, func(rt *rapid.T) {
		tokens := rapid.SliceOfN(rapid.SampledFrom(alphabet), 0, 16).Draw(rt, "tokens")
		s := strings.Join(tokens, "")
		once := Text(s)
		if Text(once) != once {
			rt.Fatalf("projection broken: once=%q twice=%q", once, Text(once))
		}
		if norm.NFC.String(once) != once {
			rt.Fatalf("canonical text is not NFC: %q", once)
		}
		for _, r := range once {
			if unicode.IsControl(r) {
				rt.Fatalf("output alphabet carries control %U in %q", r, once)
			}
		}
		if strings.ContainsRune(once, 0x1e) {
			rt.Fatalf("delimiter representable in canonical part %q", once)
		}
		if HashParts(s) != Hash(s) {
			rt.Fatalf("single-part HashParts diverges from Hash for %q", s)
		}
	})
}
