# Lint term names that shadow other terms or common words

Lands: when profile lints are extended.

`uses-term` edges are created by matching a term's name in block text, case-insensitively on
word boundaries with longest match winning. The longest-match rule resolves overlaps
correctly, but authoring a term whose name is a substring of another term name, or a common
English word, is a silent footgun: the author cannot see from the source which occurrences
will bind.

A lint should warn when a declared term name is a substring of another term name, or matches a
configurable common-word denylist, so shadowing is surfaced at compile rather than discovered
by reading the edge graph. Found authoring the cerebro corpus spec, where `law node` and
adjacent bare-`node` phrasing needed care.
