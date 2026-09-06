// Package canon defines the canonical forms hashes are computed over.
//
// Hashes are the versioning primitive of the whole system — content hashes
// version spec identities, shape hashes version binding targets — so their
// inputs must be normalization projections: applying the canonical form
// twice yields the same bytes as applying it once. Nothing here may depend
// on serialized protobuf bytes, which are not canonical.
package canon

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Text returns the canonical form of block text: control characters
// outside Unicode whitespace removed FIRST (C0 and C1 controls plus
// DEL — the whitespace controls U+0009–U+000D collapse with all other
// whitespace), then Unicode NFC, then every run of Unicode whitespace
// collapsed to a single space with leading and trailing whitespace
// removed — the operation order is REQ-model-content-hash's contract. The output
// alphabet therefore carries NO control characters — the property
// HashParts' block delimiter rests on: a raw control byte in source
// cannot survive into a canonical part, so it cannot forge a block
// boundary.
func Text(s string) string {
	// The strip PRECEDES normalization: a control between a starter and
	// a combining mark blocks canonical composition, so stripping after
	// NFC would leave a decomposed pair NFC never saw — breaking the
	// projection property (Text(Text(x)) == Text(x)) and making the
	// removal claim false one layer down (the control's former presence
	// would still change the bytes). Strip first, and the result is as
	// if the control were never there (REQ-model-content-hash's stated
	// operation order).
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(norm.NFC.String(stripped)), " ")
}

// Hash returns the content hash of block text: the SHA-256 digest of the
// UTF-8 bytes of Text(s), as 64 lowercase hexadecimal characters.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(Text(s)))
	return hex.EncodeToString(sum[:])
}

// HashParts returns the content hash of a block sequence: each part is
// canonicalized by Text, the parts are joined with U+001E (record
// separator — Text's output alphabet carries no control characters,
// its own documented contract, so the delimiter is unrepresentable
// inside a part and a raw 0x1E in source cannot forge a boundary),
// and the joined bytes are digested WITHOUT re-canonicalization. Block boundaries therefore ride
// the preimage: moving words across a boundary moves the hash even
// when the concatenated word sequence is unchanged
// (REQ-model-content-hash). HashParts(s) == Hash(s) for a single part,
// so identities without an extent keep their existing hashes.
func HashParts(parts ...string) string {
	canonical := make([]string, len(parts))
	for i, p := range parts {
		canonical[i] = Text(p)
	}
	sum := sha256.Sum256([]byte(strings.Join(canonical, "\x1e")))
	return hex.EncodeToString(sum[:])
}

// SourceDigest returns the consent-source digest of an identity: the
// SHA-256, as 64 lowercase hexadecimal characters, of the newline-joined
// SHA-256 hex digests of its parts — the consent surface's raw markdown
// blocks (the lead paragraph with its payload first, then each
// context-extent block in document order) followed by the document's
// link reference definition labels, sorted, one part each — with no
// canonicalization anywhere (REQ-model-consent-source). Raw bytes, so
// byte-identical input digests identically whatever the canonical form
// does; per-part digests, so a part boundary rides the preimage exactly
// as a block boundary rides the content hash, and no part's content
// can forge one.
func SourceDigest(blocks ...string) string {
	var joined strings.Builder
	for i, b := range blocks {
		if i > 0 {
			joined.WriteByte('\n')
		}
		sum := sha256.Sum256([]byte(b))
		joined.WriteString(hex.EncodeToString(sum[:]))
	}
	sum := sha256.Sum256([]byte(joined.String()))
	return hex.EncodeToString(sum[:])
}
