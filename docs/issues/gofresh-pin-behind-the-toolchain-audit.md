# The gofresh pin (v0.105.1) predates the toolchain audit listings the host now runs under — every judged run degrades to the unaudited posture

Filed from tugboat (2026-09-30), uncommitted for this tool's agent.

Observed: the host's primary toolchain moved to go1.27.1-dst.13 today
(the go1.27.1 port with the go/types fromRHS race fix carried; godst
release, assets attached). gofresh at v0.108.0 lists go1.27.0-dst.15,
and its working tree lists go1.27.1-dst.13 for the default and race
selections (closure/toolchainaudit.go); this tool's go.mod pins
gofresh v0.105.1, whose audit tables end earlier, so a binary built
from HEAD on the new toolchain judges every default-selection witness
as "release … is not listed" — observation admissions disabled, proofs
stripped, serving degraded to execution — on top of the dst
selection's own unwalked posture (cross-tool train chunk 297). The
installed binaries were also built by go1.27.0-dst.14 (2026-09-21);
tugboat rebuilt them from HEAD on the new toolchain today, which fixes
the binary/ambient skew but not the pin.

Need: bump the gofresh requirement to the release carrying the
go1.27.1-dst.13 listing (v0.108.0 or the next), rebuild, and reinstall.

Lands: awaiting triage.
