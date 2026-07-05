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

	"golang.org/x/text/unicode/norm"
)

// Text returns the canonical form of block text: Unicode NFC, every run of
// Unicode whitespace collapsed to a single space, leading and trailing
// whitespace removed.
func Text(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(s)), " ")
}

// Hash returns the content hash of block text: the SHA-256 digest of the
// UTF-8 bytes of Text(s), as 64 lowercase hexadecimal characters.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(Text(s)))
	return hex.EncodeToString(sum[:])
}
