package golang

import (
	"fmt"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
)

// resolutionWire renders a resolution for persistence in resolution records.
func resolutionWire(r verify.Resolution) string {
	switch r {
	case verify.Resolved:
		return "resolved"
	case verify.NotFound:
		return "not_found"
	case verify.GeneratedFile:
		return "generated_file"
	}
	return "unverified"
}

// resolutionFromWire parses a persisted resolution, reporting whether the
// value is known.
func resolutionFromWire(s string) (verify.Resolution, bool) {
	switch s {
	case "resolved":
		return verify.Resolved, true
	case "not_found":
		return verify.NotFound, true
	case "generated_file":
		return verify.GeneratedFile, true
	case "unverified":
		return verify.Unverified, true
	}
	return verify.NotFound, false
}

// classWire renders a witness class for persistence in resolution records.
func classWire(c verify.WitnessClass) string {
	switch c {
	case verify.AnalyzerProof:
		return "proof"
	case verify.PropertyWitness:
		return "property"
	}
	return "example"
}

// classFromWire parses a persisted witness class, reporting whether the
// value is known.
func classFromWire(s string) (verify.WitnessClass, bool) {
	switch s {
	case "proof":
		return verify.AnalyzerProof, true
	case "property":
		return verify.PropertyWitness, true
	case "example":
		return verify.ExampleWitness, true
	}
	return verify.ExampleWitness, false
}

// publishAccount is one publish's account under a selection key: the
// records installed and, per class a symbol was kept out by, the symbols
// themselves — a skipped symbol is named, never only counted, because an
// operator reads the account to learn which bindings pay the typed
// resolution every run and why.
type publishAccount struct {
	installed                            int
	moved, unopened, uncaptured, refused []string
}

// summed is this publish's account added to a prior publish's under the
// same key: the installed counts added, every named list joined in
// publish order — the one summing rule the account's rendering reads.
func (a publishAccount) summed(prior publishAccount) publishAccount {
	join := func(x, y []string) []string { return append(append([]string(nil), x...), y...) }
	return publishAccount{
		installed:  prior.installed + a.installed,
		moved:      join(prior.moved, a.moved),
		unopened:   join(prior.unopened, a.unopened),
		uncaptured: join(prior.uncaptured, a.uncaptured),
		refused:    join(prior.refused, a.refused),
	}
}

// closureMovedReason names the native SourceResult component that differs
// between a recorded view entry and the current capture, or the zero
// resolutionReason when those components agree.
func closureMovedReason(recorded, current gofresh.Fingerprint) resolutionReason {
	switch {
	case recorded.MaximalClosure != current.MaximalClosure:
		return resolutionClosure
	case recorded.TestVariantClosure == "" || recorded.TestVariantClosure != current.TestVariantClosure:
		return resolutionTestVariants
	case recorded.ClosureStrategy == "" || recorded.ClosureStrategy != current.ClosureStrategy:
		return resolutionClosureDerivation
	case recorded.ToolchainContent == "" || recorded.ToolchainContent != current.ToolchainContent:
		return resolutionToolchainContent
	case recorded.AdmissionPolicy == "" || recorded.AdmissionPolicy != current.AdmissionPolicy:
		return resolutionAdmissionPolicy
	case recorded.Guards.Toolchain != current.Guards.Toolchain:
		return resolutionToolchain
	case recorded.Guards.BuildConfig != current.Guards.BuildConfig:
		return resolutionBuildConfig
	}
	return resolutionReason{}
}

func closureMoved(recorded, current gofresh.Fingerprint) string {
	return closureMovedReason(recorded, current).String()
}

// Notices is the served path's account for a result: one line for the
// served and typed counts, one per selection the path degraded, and
// one per typed symbol naming why its record did not serve — advisory,
// never a verdict input.
func (s *Served) Notices() []string {
	var out []string
	typed := 0
	for _, a := range s.answers {
		if a.resolved && a.err == nil {
			typed++
		}
	}
	out = append(out, fmt.Sprintf("resolution: %d served from records, %d resolved typed", len(s.served), typed))
	for _, d := range s.degraded {
		out = append(out, "resolution degraded to typed: "+d)
	}
	out = append(out, s.publishNotices()...)
	keys := make([]string, 0, len(s.reasons))
	for k := range s.reasons {
		if a, ok := s.answers[k]; ok && a.resolved && a.err == nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, fmt.Sprintf("resolution typed: %s: %s", k, s.reasons[k].String()))
	}
	return out
}

// Reasons names, per recorded symbol that resolved typed this run, why
// its record did not serve.
func (s *Served) Reasons() map[string]string {
	out := make(map[string]string, len(s.reasons))
	for k, v := range s.reasons {
		out[k] = v.String()
	}
	return out
}

// recordSubject is the gofresh subject a record was captured for: the
// symbol's rest beneath its owning package.
func recordSubject(rec resolutioncache.Record) (gofresh.Subject, bool) {
	rest, ok := strings.CutPrefix(rec.Symbol, rec.Package+".")
	if !ok || rest == "" {
		return gofresh.Subject{}, false
	}
	return gofresh.Subject{Package: rec.Package, Symbol: rest}, true
}

// Degraded names the serving-path faults this run degraded to typed
// resolution — advisory, one line per fault.
func (s *Served) Degraded() []string { return append([]string(nil), s.degraded...) }

// ServedCount is the number of symbols answered from records this run.
func (s *Served) ServedCount() int { return len(s.served) }

// publishedNamesBound caps the symbols one class of the account line
// names; the remainder is counted.
const publishedNamesBound = 8

// namedClass renders one class of the account: its count, its cause,
// and its symbols sorted, the first publishedNamesBound named and the
// rest counted.
func namedClass(symbols []string, cause string) string {
	sorted := append([]string(nil), symbols...)
	sort.Strings(sorted)
	shown := sorted
	if len(shown) > publishedNamesBound {
		shown = shown[:publishedNamesBound]
	}
	line := fmt.Sprintf("%d %s: %s", len(sorted), cause, strings.Join(shown, ", "))
	if len(sorted) > len(shown) {
		line += fmt.Sprintf(" (+%d more)", len(sorted)-len(shown))
	}
	return line
}

// publishNotices renders the publish's account, one line per selection
// key the publish ran under: the records installed, then each class of
// symbol kept out with its symbols named (bounded).
func (s *Served) publishNotices() []string {
	keys := make([]string, 0, len(s.published))
	for key := range s.published {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []string
	for _, key := range keys {
		a := s.published[key]
		line := fmt.Sprintf("resolution published under %q: %s", selectionWord(key), countWord(a.installed, "record"))
		var skipped []string
		if len(a.moved) > 0 {
			skipped = append(skipped, namedClass(a.moved, "moved between the opening and closing capture"))
		}
		if len(a.unopened) > 0 {
			skipped = append(skipped, namedClass(a.unopened, "without an opening capture"))
		}
		if len(a.uncaptured) > 0 {
			skipped = append(skipped, namedClass(a.uncaptured, "without a closing capture"))
		}
		if len(skipped) > 0 {
			line += "; skipped " + strings.Join(skipped, "; ")
		}
		if len(a.refused) > 0 {
			line += "; " + namedClass(a.refused, "refused on their source tiers")
		}
		out = append(out, line)
	}
	return out
}

// countWord renders a count with its noun, pluralized.
func countWord(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// selectionWord spells a selection key for a person: the default
// selection by name, a tagged one by its tags and toolchain.
func selectionWord(key string) string {
	if key == "default" {
		return key
	}
	tags, toolchain, _ := strings.Cut(key, "\x00")
	if toolchain == "" {
		return tags
	}
	return tags + " " + toolchain
}
