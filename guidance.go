// Package stipulator carries the module's embedded tool-resident
// guidance: docs/guidance.md is the single home of verb-level served
// prose (what a verb does, what a knob controls, when to use which),
// embedded here because the binary travels while the repository stays
// home, and parsed once for every surface to project from
// (gofresh docs/specs/guidance.md is the format contract; this
// module's serving contract is REQ-mcp-guidance in docs/specs/mcp.md).
package stipulator

import (
	_ "embed"

	"github.com/greatliontech/gofresh/guidance"
)

//go:embed docs/guidance.md
var guidanceSrc []byte

// embeddedGuidance is the source parsed once for every surface to
// project from; Must refuses a malformed document loudly, naming this
// tool, where Document answers the parse error.
var embeddedGuidance = guidance.Embed("stipulator", guidanceSrc)

// GuidanceDocument is the embedded guidance source's parse answer —
// the parse-pinning test's seam; every face reads Guidance.
func GuidanceDocument() (*guidance.Document, error) { return embeddedGuidance.Document() }

// Guidance is the embedded guidance document every face reads: a
// malformed document is a build defect the parse-pinning test
// surfaces, so a face's construction fails loudly rather than serving
// nothing (REQ-mcp-guidance). Each knob's served text is its terse
// clause, the document's own projection (guidance.Knob.Clause).
func Guidance() *guidance.Document { return embeddedGuidance.Must() }
