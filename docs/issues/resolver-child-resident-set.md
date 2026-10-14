# The resolver child's resident set is the whole corpus closure at once

## Weaver records-only verification under a whole-tree cap

Weaver reproduced a resource refusal with installed v0.73.6 (`c01a298`) using
`stipulator verify --no-test`, after the accepted root and nested-module race suites
had passed outside Stipulator. No test execution was requested by the failing command.

The first command ran in a user systemd scope with `MemoryMax=2677182464`,
`MemorySwapMax=0`, `RuntimeMaxSec=7200`, and `GOFLAGS=-p=8`. It emitted only the
compile-start diagnostic, then exited by signal 9 after 19.1 seconds. The systemd
journal explicitly reported a kernel OOM kill and a 2.4 GiB scope peak.

A separate attempt retained a hard 2 GiB scope, disabled swap, and set
`GOMEMLIMIT=512MiB GOFLAGS=-p=2`. It reached discovery after 1m22.1s, reporting
260 MiB resident and a 1.9 GiB peak; the scope was OOM-killed at 2m00s with a 2 GiB
scope peak. The Go heap target is not a process-tree hard bound. These measurements
do not attribute the peak to an individual resolver, compiler or parent process.

Both runs are incomplete hygiene verification, not passing records checks.
External suites and contract compilation are not substitutes for the missing binding
and pin verdict. A bounded records-only workflow is needed without rerunning tests
or weakening the consumer's process-tree memory protection.

The served Go resolver child (`stipulator internal-resolve`, the self-exec'd
process behind `verify`, `check` and `pin`) loads the typed packages of every
binding's closure into one process for the whole pass. Its resident set is
the corpus's package set, held until the pass ends, and on a mid-size corpus
it is the largest single allocation on the host.

Field report (greatliontech/cerebro, stipulator 8fec2b4 and 9209125,
2026-09-22): `stipulator verify` over a corpus of 6 documents, 435
requirements and 2,249 bindings across the root module, a kernel module and a
sim module (about 101 root packages). The child was observed at 3,162,928 kB
RSS 155 s into the pass (snapshot, not the peak; the process ended before its
VmHWM could be read). On a 30 GiB host whose swap (4 GiB) was already full,
two consecutive `verify` runs launched beside one package-scoped
`go test -p 2` were killed by the host's memory guard (exit 137, signal 9)
before reporting; the same command alone on the same tree completes in about
90 s and reports green. Resident baselines on that host are three language
servers (2.6 GB together) and an idle gomutant MCP server (2.3 GB), which is
what leaves the 3 GB spike no room.

What the field observed, not a diagnosis: the resident set is proportional
to the closure, not to what moved (a warm verify that executes nothing still
pays it), and there is no knob bounding it. Two shapes the tool could take,
the choice being the tool's: slice the resolution per module or per package
group with the programs released between slices (the shape gomutant's
observed-union issue sketches for its own pass), or bound the child under a
ceiling derived from the host's memory as the oracle runs already are, so a
pass that would exceed it refuses stated rather than dying under the host's
guard with no verdict written.

Second field report (greatliontech/cerebro, stipulator binary of 2026-09-22,
2026-10-04): `stipulator check` over the same corpus grown to 7,093 subjects
in 111 packages, run alone after a green `go test -p 2 ./...` sweep on the
same 30 GiB host. The resolver child was observed at 4,267,820 kB RSS (first
run, 3m33s in) and 4,714,064 kB (second run, 27 s in, before any test
executed) — snapshots, not peaks. The parent `check` process itself was
observed at 6,935,604 kB RSS 4m50s into the first run, during the execution
phase, with 111 packages' `go test -json` children running beneath it; the
host reached zero available memory and the run was interrupted by hand
before the host's guard chose a process. The parent's growth is a second
resident set the paragraph above does not describe: the child's closure
lives through execution (the first run's child was alive 3m33s in, past
discovery), and the parent's set grows with the executing packages' output.
Resident baselines on the host at the time: three opencode sessions
(3.9 GB together), a terminal (1.4 GB), gopls (1.3 GB), and a second
`stipulator check` of another session on a different module, with its own
resolver child.

What chunk 307 settled (2026-10-05): the resolver child's half is
resolved — the pass asks every question of the child at discovery,
publishes its records and releases the child before the first process
spawns (a check over gofresh opened execution with no descendant where
the child had held 475 MiB through two hours); the parent runs under a
soft ceiling derived from the host, the package spawn bound gained a
memory term reading the host's available memory and the pass's own
trees, and a pass the host cannot hold one process of refuses stated.
The parent's growth during execution did NOT reproduce here: a full
self-host pass over 31 packages (one race invocation, twelve children
at the open) held the parent at 126–181 MiB through fifty-one minutes
of execution; its peak, 987 MiB, is discovery's engines and views,
released at discovery's exit. The 6.9 GB parent beside 111 children
remains the field's observation alone, and every face now states the
resident datum at each phase transition and in the pass's timing line.

Stash observation (2026-10-12, `c9ea6cd`): a `stipulator mcp` server running
one `check` pass whose only live child was a single `go test -race -json`
invocation of `internal/sh/interp` (35 to 60 minutes into that child, no
resolver child present) read 2.43 GiB and 2.14 GiB VmRSS two minutes apart,
VmHWM 3.09 GiB, 2.3 GiB of it anonymous Go heap; the process read 182 MiB
once the pass ended. A second pass in the same server reported its own
resident as 182 MiB with a 218 MiB phase peak in that window. The MCP
summary face of either pass carried no resident or timing line, so the
per-phase datum this issue lands on was not available from the MCP result;
the readings above are from `/proc`. This is the parent-growth-during-
execution shape with one child, not 111. Full report in the
[completed-package peak](completed-package-peak-admission.md) issue.

Lands: the next cerebro report carrying the pass's own resident lines
(the parent's reading at execution's exit and at the end, and the
timing line's running peak with its moment) — the datum that attributes
the growth to a phase before any mechanism is built for it.

The [Cerebro scoped-discovery report](cerebro-scoped-discovery-cost.md) supplies
further phase-owned readings: an older-build completed run ended at 355 MiB with
a 1.1 GiB peak, while cold baseline discovery reached a 7.4 GiB peak; an explicit
c9ea6cd single-requirement check ended in discovery at 600 seconds, 5.0 GiB
resident and 5.3 GiB peak. The builds, input states and cache conditions differ.
This evidence does not reproduce the earlier execution-parent growth; investigate
discovery attribution separately rather than treating every peak as the same fault.
