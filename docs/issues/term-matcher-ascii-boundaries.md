# Term matching uses ASCII word boundaries — non-ASCII term names may never match

Lands: when a corpus declares non-ASCII term names.

`newTermMatcher` builds `\b` + QuoteMeta(name) + `\b` per term, and Go's
`\b` is ASCII-only: a term name beginning or ending in a non-ASCII rune
(é, Greek, CJK) can fail to match at any use site, silently dropping
uses-term edges. The opt-in term lint deliberately mirrors the same
ASCII semantics (`isWordByte`), so warnings stay consistent with actual
binding behavior — fixing one means fixing both together, on
`unicode.IsLetter`/`IsDigit` boundaries over runes.

Concrete exposure: a law corpus with native-language term names. Found
adjacent to the term-lint work; pre-existing in the matcher since term
matching landed.
