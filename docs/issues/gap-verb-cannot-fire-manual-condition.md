# Gap verb cannot mark a manual landing condition fired

Lands: when the gap verb's input surface next changes, or when a manual-condition gap in this corpus first needs firing.

## Observed

A manual landing condition fires only when its record's `fired` bit is explicitly marked (REQ-gap-conditions), and that bit is now lifecycle-bearing: an unfired manual gap on a covered requirement stays open, and firing it is what resolves the record into prune residue (REQ-gap-lifecycle). The gap verb builds landing conditions from `--covered`/`--exists`/`--manual` and never sets `fired` (`author.NewLandingCondition`), so the validated-write path cannot express the one field whose flip changes a record's lifecycle; firing today requires editing the committed textproto directly. Worse, re-declaring an already-fired gap through the verb rewrites the record unfired — the retarget note surfaces the change, but the verb offers no way to preserve or set the fired state.

## Resolution

Give the verb an explicit firing affordance — a flag alongside `--manual`, or a dedicated operation — that preserves REQ-gap-verb's update-in-place validation and retarget surfacing, so an external judgment enters the record system through the same validated path as the declaration it discharges.
