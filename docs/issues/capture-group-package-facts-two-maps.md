# A capture group's package facts live in two maps that must agree

captureGroup.tests (package → expected test names) and
captureGroup.pkgInv (package → covering invocation) are written
together at discovery and read together everywhere a package's
coverage is judged — the completion tracker (newGroupTracker's `!ok`
skip), the publish judgment, the selective form's executing predicate
— the tracker's read still guarding a disagreement the writer never
produces (a package in one map and not the other). One map of package entries
{invocation, names} makes the disagreement unrepresentable and deletes
the guards.

Lands: cross-tool train chunk 227 (the stipulator sweep).
