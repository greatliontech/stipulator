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

Lands: the cross-tool train's next triage gate.
