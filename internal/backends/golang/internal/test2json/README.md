# Owned Go test-output conversion

Adapted from Go **go1.27.2**, `src/cmd/internal/test2json/test2json.go` and its
`testdata/` corpus: https://github.com/golang/go/tree/go1.27.2/src/cmd/internal/test2json.
The original source SHA-256 is
`81b811cc3af1cc49da5faec1a93aabf12b769a69a5dbc60505c125edb19d288b`.
Copyright belongs to The Go Authors; the upstream BSD three-clause license is
included in `LICENSE`. No Go internal package or additional module is required.

The decoder owns the explicit Go framing, escaping, diagnostic and JSON event
protocol. The input scan resumes at its last examined byte, so an incomplete
buffer is not rescanned under its own ending escape state. Input and output
buffers remain fixed-size; long lines stream in UTF-8-safe fragments.
Framing after a streamed long prefix uses logical-line state, including a real
marker arriving at offset zero of a fresh buffer.
Error-marker transitions resume at the first remaining byte, so adjacent escapes
and error markers retain their protocol meaning.

At EOF, unfinished input is emitted as output before already-parsed reports
flush. Only a complete package framing line establishes the package terminal.
There is no process-exit-to-terminal API. Write failures are sticky, short writes
are errors, and closing is idempotent. The execution owner separately judges
binary exit, original harness completion and capture validity.

The upstream corpus remains unmodified. Tests retain its exact event and output
expectations where applicable and separately specify the corrected EOF behavior
where upstream omitted already-parsed reports.
The older `attr.json` golden omits `OutputType`; its framing lines are asserted
with `OutputType: frame`, as emitted by the imported implementation.
