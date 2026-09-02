package golang

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Runner-environment inspectability: a witness that fails only inside
// the runner — while direct `go test` stays green — is failing on
// something the runner's execution environment contributes: an
// inherited or pinned variable, the working directory, the narrowed
// parallelism width, a process limit. That correlated variable is
// invisible in a bare failure, so the operator guesses. On a failure
// that flips prior passing witness evidence, the diagnostic carries
// the runner's execution-environment report
// (REQ-evidence-flip-environment): every candidate variable named,
// derived from the SAME environment derivation the spawn used
// (witnessEnvOf, diffed against the ambient sample the normalization
// consumed — spawn, ingest, and report are one environment), so the
// report can never describe an environment the process did not see.

// declaredOverrideKeys extracts the keys of the invocation's own
// declared environment overrides — the exact set whose ambient
// counterpart must never render: the override may exist precisely to
// shadow it (a test credential over a real one). The invocation
// carries the declared set verbatim, so membership is tested against
// the committed record itself — no hand-maintained key list to drift
// from the normalizer's actual pins.
func declaredOverrideKeys(n *NormalizedInvocation) map[string]bool {
	keys := make(map[string]bool, len(n.EnvOverrides))
	for _, kv := range n.EnvOverrides {
		if k, _, ok := strings.Cut(kv, "="); ok {
			keys[k] = true
		}
	}
	return keys
}

// envValueCap bounds each rendered environment value: a candidate
// variable needs to be NAMED with enough prefix to identify it, never
// reproduced — an unbounded value (a config blob, a PEM) rendered in
// full would let the report's size be negotiated against the failure
// output it must stand beside, and every after-the-fact reconciliation
// of that negotiation starves one side or the other.
const envValueCap = 192

// reportCap bounds the whole rendered report structurally below the
// failure diagnostic's cap, so the failure output always keeps most of
// its room: with values prefix-capped the report is proportional to
// key count, and a pathological key population still cuts off here
// rather than eating the failure text.
const reportCap = failureOutputCap / 4

func envValue(v string) string {
	if len(v) <= envValueCap {
		return v
	}
	return fmt.Sprintf("%s… [%d bytes total]", truncateValidUTF8(v, envValueCap), len(v))
}

// truncateValidUTF8 cuts s to at most limit bytes without splitting a
// rune, and scrubs invalid sequences: environment values and process
// output are bytes, and the diagnostic rides an edition-2023 proto
// string field whose UTF-8 validation refuses the whole message at
// marshal — a garbled byte must never convert a verdict into a
// serialization fault.
func truncateValidUTF8(s string, limit int) string {
	// Scrub before cutting: the replacement rune is wider than the
	// byte it replaces, so scrubbing after the cut could expand the
	// result past the limit — the cut then operates on valid text and
	// the boundary backoff is exact.
	s = strings.ToValidUTF8(s, "�")
	if len(s) > limit {
		for limit > 0 && !utf8.RuneStart(s[limit]) {
			limit--
		}
		s = s[:limit]
	}
	return s
}

// envDivergenceReport renders one invocation's runner execution
// environment for a verdict-flipping witness failure: the working
// directories (the go command's and the failing package's), the
// delivered parallelism width and fan-out exactly as the invocation
// froze them at normalize, the curated-vs-ambient delta (runner-set
// and runner-changed entries with prefix-capped values, the ambient
// counterpart rendered only for the runner's own pins, dropped entries
// by name), and the process resource limits. Deterministic for a fixed
// invocation and bounded at render time to a fraction of the failure
// diagnostic's cap.
func envDivergenceReport(n *NormalizedInvocation, pkgDir string) string {
	var out strings.Builder
	cur := envIndex(witnessEnvOf(n))
	amb := envIndex(ambientOf(n))
	out.WriteString("verdict flips prior passing witness evidence; runner execution environment (candidate variables for a runner-correlated failure):\n")
	binDir := pkgDir
	if binDir == "" {
		binDir = "its package directory"
	}
	fmt.Fprintf(&out, "  go test spawned in %s; the test binary runs in %s with PWD pinned to it\n", n.Dir, binDir)
	width := cur["GOMAXPROCS"]
	if width == "" {
		width = "unpinned"
	}
	fmt.Fprintf(&out, "  delivered parallelism width: GOMAXPROCS=%s (package processes fan out %d wide)\n", width, spawnBoundOf(n))
	declared := declaredOverrideKeys(n)
	for _, k := range sortedKeys(cur) {
		av, inAmbient := amb[k]
		switch {
		case !inAmbient:
			fmt.Fprintf(&out, "  %s=%s (runner-set, absent in the ambient environment)\n", k, envValue(cur[k]))
		case av == cur[k]:
		case declared[k]:
			// A changed key the invocation declared: the runner-side
			// value is committed policy text, but the ambient value it
			// shadows may be exactly what the override keeps out of
			// view.
			fmt.Fprintf(&out, "  %s=%s (declared override; ambient value withheld)\n", k, envValue(cur[k]))
		default:
			// A changed key the invocation did not declare is the
			// runner's own pin — a toolchain or width fact, safe from
			// both sides.
			fmt.Fprintf(&out, "  %s=%s (runner; ambient %s=%s)\n", k, envValue(cur[k]), k, envValue(av))
		}
	}
	for _, k := range sortedKeys(amb) {
		if _, ok := cur[k]; !ok {
			fmt.Fprintf(&out, "  %s dropped by the runner\n", k)
		}
	}
	if lim := processLimits(); lim != "" {
		out.WriteString("  process limits: " + lim + "\n")
	}
	// The whole report scrubs once — keys and short values are bytes
	// too — and the cap cut is rune-safe, so the report can never carry
	// the invalid UTF-8 an edition-2023 proto string field refuses.
	rep := strings.ToValidUTF8(out.String(), "�")
	if len(rep) > reportCap {
		rep = truncateValidUTF8(rep, reportCap) + "\n  [environment report truncated]"
	}
	return rep
}

// ambientOf is the report's ambient leg: the sample the invocation's
// normalization actually consumed, so the delta describes the exact
// derivation that ran. The report-time fallback exists only for
// hand-built invocations in tests.
func ambientOf(n *NormalizedInvocation) []string {
	if n.Ambient != nil {
		return n.Ambient
	}
	return os.Environ()
}

func envIndex(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// enrichFlipDiagnostics appends the runner's execution-environment
// report to per-test failure diagnostics whose subject holds prior
// passing witness evidence — the verdict-flip class
// (REQ-evidence-flip-environment) — at most once per subject per
// invocation: a flipped test the isolation pass also re-ran solo
// carries two diagnostics for one environment and the report rides the
// first, while the same subject failing under a second invocation
// failed under a second curated environment, which renders its own
// report. The enriched set is caller-owned, so the dedup spans every
// merge of one run. A failure with no prior pass is not a flip and
// stays bare; package-scoped diagnostics and non-failure dispositions
// are untouched. The combined output stays within the diagnostic's own
// cap with the failure output yielding room to the report — the
// failure text is the unbounded side, and a report starved to nothing
// by a huge dump would go silent exactly where it was commissioned to
// speak — and truncation marked on the diagnostic's typed field
// exactly as execution marks it.
func enrichFlipDiagnostics(m *execMerge, normalized map[string]*NormalizedInvocation, passedBefore map[string]bool, enriched map[string]bool) {
	if m == nil {
		return
	}
	reports := map[string]string{}
	for _, d := range m.diags {
		if d.GetTest() == "" || d.GetDisposition() != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED {
			continue
		}
		if !passedBefore[d.GetPackage()+"."+d.GetTest()] {
			continue
		}
		key := d.GetInvocation() + "\x00" + d.GetPackage() + "." + d.GetTest()
		if enriched[key] {
			continue
		}
		n := normalized[d.GetInvocation()]
		if n == nil {
			continue
		}
		repKey := d.GetInvocation() + "\x00" + d.GetPackage()
		rep, ok := reports[repKey]
		if !ok {
			rep = envDivergenceReport(n, n.PkgDirs[d.GetPackage()])
			reports[repKey] = rep
		}
		// The report is render-bounded well below the cap, so the room
		// here is structurally positive — the failure output keeps at
		// least three quarters of the diagnostic.
		room := failureOutputCap - len(rep) - 1
		orig := d.GetOutput()
		truncated := false
		if len(orig) > room {
			orig = truncateValidUTF8(orig, room)
			truncated = true
		}
		combined := orig
		if combined != "" {
			combined += "\n"
		}
		combined += rep
		d.SetOutput(combined)
		if truncated {
			d.SetTruncated(true)
		}
		enriched[key] = true
	}
}
