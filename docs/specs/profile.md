# Authoring profile

The profile is the grammar of the corpus. Goldmark supplies the block
structure; the profile assigns spec-model meaning to native markdown elements,
with a single micro-convention — the strong-emphasis lead-in — carrying
identity and metadata. The linter is this grammar's error channel. Everything
the compiler recognizes is listed here; prose outside these structures is
non-normative by construction.

## Corpus and manifest

**REQ-profile-manifest** (behavior): The compiler MUST read the manifest from
`.stipulator/manifest.textproto` and fail when it is absent; the manifest
declares at least the corpus include globs, defaulting to
`docs/specs/**/*.md`.

**REQ-profile-root** (behavior): Commands MUST locate the corpus root by
searching upward from their working directory to the nearest ancestor
containing `.stipulator/manifest.textproto` — nearest wins, so corpora
nest — failing with guidance when no ancestor has one. Discovery belongs
to the command surface: the core always operates on an
already-rooted tree.

**REQ-profile-enumeration** (behavior): The corpus MUST be enumerated by
resolving the manifest's include globs and sorting the resulting paths
lexicographically; generated folder indexes are excluded from enumeration.

**REQ-profile-glob** (behavior, refines REQ-profile-enumeration): An include
glob MUST be matched against slash-separated paths relative to the repository
root, per path segment — `**` as a complete segment matching zero or more
segments; any other segment matching exactly one path segment, with `*` (a
possibly empty run of characters), `?` (any single character), `[...]` and
`[^...]` classes with `-` ranges (an inverted range is valid and matches
nothing),
and `\` escapes — and a glob containing an empty or malformed segment is
rejected when enumeration begins, before any matching.

## Documents and sections

**REQ-profile-utf8** (behavior): A corpus document that is not valid UTF-8
MUST be rejected at compile time, before any hashing or matching.

**REQ-profile-doc-title** (behavior): Each corpus document MUST contain
exactly one level-1 heading, which is the document title; subsequent headings
form the section tree.

## Requirement and term leads

**REQ-profile-requirement-lead** (behavior): A paragraph whose first inline
is a strong-emphasis span matching the identifier grammar, followed by a
parenthetical metadata clause and a colon, MUST compile to a requirement
whose text is the remainder of the paragraph together with its payload.

**REQ-profile-id-grammar** (behavior): A requirement identifier MUST match
`REQ(-[a-z0-9]+)+` and be unique across the corpus and the tombstone
registry.

**REQ-profile-metadata** (behavior): The metadata parenthetical MUST consist
of comma-separated clauses — first the clause kind, one of `behavior`,
`invariant`, `structural`, or `wire`, then optionally edge clauses `refines`,
`depends`, or `supersedes`, each followed by one or more space-separated
requirement identifiers — with any unknown kind or malformed clause a compile
error.

**REQ-profile-lead-nearmiss** (behavior): A paragraph beginning with a
strong-emphasis span that matches the identifier grammar but does not parse
as a requirement lead MUST be a compile error, so that a malformed lead can
never silently demote a requirement to prose.

**REQ-profile-term-lead** (behavior): A paragraph whose first inline is a
strong-emphasis span followed by `(term):` MUST compile to a term whose
identity is its name, unique case-insensitively across the corpus and the
tombstone registry.

## Payloads and notes

**REQ-profile-payload** (behavior): A contiguous run of list and table blocks
immediately following a requirement's lead paragraph MUST be the
requirement's payload — part of its text for keyword detection, reference
detection, term matching, and content hashing.

**REQ-profile-note** (behavior): A blockquote MUST compile to a non-normative
note attached to the immediately preceding requirement or term in the same
section, or to the enclosing section when none precedes it.

## Inert content

**REQ-profile-code-inert** (behavior): Code spans and code blocks MUST be
exempt from normative-keyword detection, identifier-reference detection, and
term matching.

## Normative keywords

**REQ-profile-one-keyword** (behavior): The text of a requirement MUST
contain exactly one normative keyword occurrence — drawn from `MUST`,
`MUST NOT`, `SHOULD`, `SHOULD NOT`, `MAY`, matched uppercase on word
boundaries, with the two-word forms counting as single occurrences.

**REQ-profile-orphan-keyword** (behavior): A normative keyword occurring
outside requirement text MUST be a compile error.

## References

**REQ-profile-term-matching** (behavior): The compiler MUST create a
`uses-term` edge from a requirement or term to every term whose name occurs
in the block's text, matched case-insensitively on word boundaries with
longest match winning.

**REQ-profile-id-reference** (behavior): A requirement identifier token
occurring in any block's text MUST compile to a `reference` from that block
to the identified node, with an identifier that resolves to nothing a
compile error.

## Non-normative content

**REQ-profile-annotations** (behavior): Content that is neither a requirement
nor a term nor a note MUST compile to an annotation node attached to its
enclosing section, preserved in the IR and carried into bundles for context.

## Generated indexes

**REQ-profile-index-generated** (behavior): The `fmt` operation MUST write,
for each directory containing at least one corpus document, a `README.md`
index generated from the IR, containing no normative text.

**REQ-profile-index-fresh** (behavior): Lint MUST reject a repository whose
generated indexes differ from what `fmt` would regenerate.
