# Installed stipulator binary lags the repo HEAD (fleet sweep 2026-08-27)

The weekly fleet sweep's binary-provenance check found the installed
`stipulator` binary's `vcs.revision` (b667e8f241ee) behind the repo
HEAD (143be37af416). The skew guards refuse loudly at use; the sweep's
job is to catch the drift before a session trips on it, and this doc
is that catch made durable.

Undiagnosed whether the lag is a missed install at the last landed
change set's close (the binary is rebuilt from the landed HEAD as part
of every change set that ships) or a deliberate hold at an earlier
revision; either way the installed binary is not the reviewed HEAD,
and every `stipulator check` verdict on this machine — the seven
estates the sweep judges, and every consumer's change-set gate — is
produced by the older code.

Lands: cross-tool train chunk 134 (stipulator check joins gomutant's
change-set gate and pew adopts; those gates run the installed binary,
so the chunk's first action installs it at the landed HEAD, and the
sweep's next report confirms `match`).
