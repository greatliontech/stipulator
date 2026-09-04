package stipulator

import (
	"regexp"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guidance"
	"github.com/greatliontech/stipulator/stipulate"
)

// readers holds, per surface, the whole-word matchers of that surface's
// reader markers: a reason on a single-surface element must name THAT
// surface's reader, not merely some reader (REQ-mcp-surfaces).
var readers = map[string][]*regexp.Regexp{
	"cli": markers("operator", "CI", "script", "scripts", "shell", "positional"),
	"mcp": markers("agent", "token"),
}

func markers(words ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(words))
	for _, w := range words {
		out = append(out, regexp.MustCompile(`\b`+regexp.QuoteMeta(w)+`\b`))
	}
	return out
}

// namesReader reports whether text names surface's reader as a whole
// word — "script" inside "description" is no reason.
func namesReader(text, surface string) bool {
	for _, marker := range readers[surface] {
		if marker.MatchString(text) {
			return true
		}
	}
	return false
}

// TestGuidanceNamesTheReaderOfEverySingleSurfaceElement pins
// REQ-mcp-surfaces over the embedded guidance document: every verb that
// exists on one surface only names that surface's reader in its
// decision prose, and every knob declared for one surface under a
// two-surface verb names that surface's reader in its own prose — so an
// opt-in with no purpose cannot stay in the document unnoticed. A knob
// of a single-surface verb inherits the verb's reason. The document is
// embedded and parsed in-process: the test's inputs are the source
// closure the fingerprint already pins (REQ-purity-responsibility).
//
//gofresh:pure
func TestGuidanceNamesTheReaderOfEverySingleSurfaceElement(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-surfaces")
	doc, err := GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	single, unreasoned := unreasonedElements(doc)
	if single == 0 {
		t.Fatal("no single-surface element found; the walk pins nothing")
	}
	if len(unreasoned) != 0 {
		t.Fatalf("single-surface elements naming no reader of their surface: %s", strings.Join(unreasoned, ", "))
	}
	// The judgment is real, on both kinds of element: a single-surface
	// verb with no reader, a single-surface knob with none, and a knob
	// whose reason names the other surface's reader are each caught,
	// while a marker inside another word is no reason.
	synthetic := &guidance.Document{Verbs: []guidance.Verb{
		{Name: "solo", Surfaces: []guidance.SurfaceName{{Surface: "cli", Name: "solo"}}, When: "use solo to do a thing."},
		{Name: "both", Knobs: []guidance.Knob{
			{Name: "bare", Surfaces: []guidance.SurfaceName{{Surface: "mcp", Name: "bare"}}, Text: "a bare knob."},
			{Name: "inword", Surfaces: []guidance.SurfaceName{{Surface: "cli", Name: "inword"}}, Text: "a knob with a description."},
			{Name: "crossed", Surfaces: []guidance.SurfaceName{{Surface: "cli", Name: "crossed"}}, Text: "for the agent's token economy."},
			{Name: "fine", Surfaces: []guidance.SurfaceName{{Surface: "cli", Name: "fine"}}, Text: "exit code only, for CI."},
		}},
	}}
	if n, got := unreasonedElements(synthetic); n != 5 || strings.Join(got, ", ") != "solo (cli verb), both.bare (mcp), both.inword (cli), both.crossed (cli)" {
		t.Fatalf("synthetic elements judged %d, unreasoned %v", n, got)
	}
}

// unreasonedElements walks doc for every element that exists on one
// surface only — a single-surface verb, judged by its decision prose,
// or a single-surface knob of a two-surface verb, judged by its own
// prose (a single-surface verb's knobs inherit its reason) — and
// returns how many it judged and which name no reader of their surface.
func unreasonedElements(doc *guidance.Document) (int, []string) {
	single := 0
	var unreasoned []string
	for _, verb := range doc.Verbs {
		if len(verb.Surfaces) == 1 {
			single++
			surface := verb.Surfaces[0].Surface
			if !namesReader(verb.When, surface) {
				unreasoned = append(unreasoned, verb.Name+" ("+surface+" verb)")
			}
			continue
		}
		for _, knob := range verb.Knobs {
			if len(knob.Surfaces) != 1 {
				continue
			}
			single++
			surface := knob.Surfaces[0].Surface
			if !namesReader(knob.Text, surface) {
				unreasoned = append(unreasoned, verb.Name+"."+knob.Name+" ("+surface+")")
			}
		}
	}
	return single, unreasoned
}
