# Retained-driver execution on foreign platforms remains unverified

Lands: tooling-redesign 9.3, under its execution authorization and prerequisite gates.

The retained driver owns the binary process and in-process output conversion
separately and observes termination without reaping the producer before its
process-tree cleanup.
Linux execution, late-exit and stopped-child regressions pass. Source review and
cross-compilation/vet cover Darwin, FreeBSD, NetBSD, DragonFly, OpenBSD,
Solaris/illumos and Windows, but no native execution on those platforms was
available. This is an evidence limit, not a demonstrated remaining runtime fault.

On authorized native runners, execute the retained-driver regressions for normal
exit, observation after exit, a stopped child resumed to exit 7, final reap,
caller cancellation after envelope expiry, converter drainage and descendant
cleanup. Exercise the platform-specific observations and calling conventions:
Darwin/OpenBSD process-state queries, BSD `wait6`, Solaris/illumos `waitid`, and
Windows process handles/tree cleanup. Preserve the actual binary status and
process identity independently of converter success.

`TestRetainedDarwinObserverAttachesAfterZombie` must establish `SZOMB` before
its first observer attachment. Stopped-child preconditions must leave the stop
notification available; no-event returns cannot establish a stop from status
bytes alone. Run the common observer and lifecycle cases on every commissioned
target rather than treating another operating system's passing result as its
evidence. Keep unsupported fixture capabilities explicit.

Cross-compilation establishes build/link compatibility and vet checks static
properties; neither supplies a native-runtime pass. Retain exact platform,
architecture, toolchain and command results with acceptance, and repair any
reproduced discrepancy without weakening ownership or completion premises.
