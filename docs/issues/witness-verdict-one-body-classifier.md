# The witness classifier asks "does this body drive a runner" in three places

`classifyWitness` inspects the resolved view's bound body once for the
proof, property, near-miss, and dot-import facts; the seeding walk asks
every other view's body and every helper body the narrower question
through `bodyDrivesRunner`; the class is the resolved view's, the
serving answer a union over views computed beside the class switch. The
union sat inside the example branch until the review found the proof
and fuzz branches bypassing it — the asymmetry between the rich pass
and the narrow one is what let that class of fault in twice.

The collapse: one `classifyBody(selection, function, declaration,
package)` returning the full verdict for ONE view — class, near-miss
reason, direct seeding, transitive hop, refusal — with the symbol-level
verdict a fold over the views holding the symbol (the resolved view's
class, the union of every view's seeding). The three spellings become
one; a new verdict field cannot be carried by one branch and dropped by
another.

Lands: cross-tool train chunk 226
to the serving refusal set.

Audit 334 (2026-10-07): `bodyDrivesRunner` was deleted at 298 — `bodyFactsOf` is the one body memo (drives, the driver's site, the callees at their sites) and the classifier's passes are two: classifyWitness's own inspection and the walk's memo. The premise is re-derived at 226's open.
