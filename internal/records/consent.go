package records

import stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"

// RehashNote is the one spelling of what a rehash is, on every surface
// that names one.
const RehashNote = "text unchanged; the canonical form moved"

// Hashes is the compiled corpus's consent surface: per requirement, its
// content hash and its consent-source digest — the one pair every
// consent judgment reads (REQ-evidence-consent-current).
type Hashes struct {
	Content map[string]string
	Source  map[string]string
}

// HashesOf collects the consent surface of a compiled corpus.
func HashesOf(spec *stipulatorv1.Spec) Hashes {
	h := Hashes{Content: map[string]string{}, Source: map[string]string{}}
	for _, r := range spec.GetRequirements() {
		h.Content[r.GetId()] = r.GetContentHash()
		h.Source[r.GetId()] = r.GetSourceHash()
	}
	return h
}

// Known reports whether the corpus declares the requirement.
func (h Hashes) Known(id string) bool {
	_, ok := h.Content[id]
	return ok
}

// Judge judges a record's pins against the requirement's current
// consent surface.
func (h Hashes) Judge(id, contentPin, sourcePin string) Consent {
	return JudgeConsent(contentPin, sourcePin, h.Content[id], h.Source[id])
}

// Consent is one record's consent to its requirement's current text,
// judged over the two pins (REQ-evidence-consent-current).
type Consent int

const (
	// Stale: the content pin differs and the source pin is unset or
	// differs — the text may have moved; re-consent is a named ceremony.
	Stale Consent = iota
	// Current: the content pin equals the requirement's content hash.
	Current
	// Rehash: the content pin differs but the source pin equals the
	// requirement's consent-source digest — byte-identical text, so the
	// canonical form moved, not the text. Current for every judgment;
	// the blanket pin rewrites the content pin without consent.
	Rehash
)

// Holds reports whether the consent is current (plain or by rehash).
func (c Consent) Holds() bool { return c != Stale }

// JudgeConsent judges a record's pins against the requirement's current
// hashes. An unset content pin is stale (never pinned); an unset source
// pin (a pre-field record) judges by the content pin alone.
func JudgeConsent(contentPin, sourcePin, contentHash, sourceHash string) Consent {
	switch {
	case contentPin == "":
		return Stale
	case contentPin == contentHash:
		return Current
	case sourcePin != "" && sourcePin == sourceHash:
		return Rehash
	}
	return Stale
}
