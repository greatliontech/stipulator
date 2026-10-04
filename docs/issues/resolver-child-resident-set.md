# The resolver child's resident set is the whole corpus closure at once

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

Lands: cross-tool train chunk 249 (the monoliths — check.Run's ladder
and the served resolver form; the child's whole-corpus resident set is
slotted as a rider at the 2026-09-29 replan).