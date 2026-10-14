# Package and test timing diagnostics across cancellation

Lands: the next Go execution-reporting change exercising a cancelled multi-package
policy invocation.

## Observed diagnostic gap

Weaver's accepted policy executes the root module and a separate ProtoDB consumer
module under the race detector. Installed Stipulator v0.73.6 at `c01a298` reports
phase durations and aggregate package-completion counts, but the retained output
does not identify which packages or tests account for long silent execution gaps.

One completed pass reported:

```text
phase execution (30.3s)
offline-race: 59/62 packages (2m41.6s)
offline-race: 60/62 packages (5m46.5s)
offline-race: 61/62 packages (14m19.6s)
offline-race: 62/62 packages (20m34.1s)
protodb-consumer-race: 2/2 packages (20m40.3s)
check: pass
```

A subsequent cancelled pass reported execution beginning at 5m9.2s, 12/62 packages
completed at 5m57.6s, then 13/62 at 23m15.7s. On cancellation the counter advanced
to 62/62 and the footer correctly said `cancelled in the execution phase; kept
nothing`. The ending count does not establish successful package or test completion.

The Go executor consumes `go test -json`, but its decoded event shape does not
retain `Time` or `Elapsed`. Witness records retain outcomes/fingerprints rather than
a timing history. These observations therefore cannot distinguish slow test bodies,
compilation/linking, packages waiting for memory admission, or process termination.

The memory-attribution defect in `completed-package-peak-admission.md` and the
freshness problem in `nested-module-freshness-root.md` are independently tracked.
Correcting those does not by itself supply the missing execution timeline.

## Requested diagnostic capability

Expose machine-readable, invocation- and package-labelled diagnostics that allow a
consumer to reconstruct:

- Package admission request, actual start, exit, and time spent queued, with the
  existing admission-origin reason where a package waits or refuses.
- Test start/end and elapsed time, preserving the distinction between nested tests
  and package totals so overlapping durations are not incorrectly summed.
- Completed, failed, skipped, cancelled, and never-started work as distinct states.
- Partial diagnostic output on cancellation, without publishing incomplete runs as
  witnesses, changing the verdict, or treating a completion counter as a pass tally.
- Attribution of timing to discovery, build/driver work and test execution where
  supported; explicitly identify portions that cannot be separated from the data.

Diagnostics may be bounded or exported separately from the normal summary. Any
truncation must identify omitted data. They must remain separate from reusable
correctness evidence: a diagnostic trace is not an additional acceptance path.

## Acceptance evidence

Use a fixture policy containing a fast package, a deliberately blocked package and
a package waiting for admission. Cancel the invocation after observing the known
states. Its diagnostics must identify those states and retain the fast package's
test timing while the check remains incomplete. A successful paired run must expose
the slow package/test identity rather than only an aggregate counter. Preserve
existing memory protection and cancellation semantics.
