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
// nothing (REQ-mcp-guidance). Each served string is one of gofresh's
// projections of the document — the registration, the knob usage, the
// schema rendering — read through the accessors below.
func Guidance() *guidance.Document { return embeddedGuidance.Must() }

// GuidanceKnob is a verb's knob under a face's spelling, refusing a
// knob the document does not carry with the package's wording
// ("stipulator: guidance: …") — the CLI reads Usage (pflag's grammar),
// the wire the schema rendering (REQ-mcp-guidance).
func GuidanceKnob(face, verb, name string) guidance.Knob {
	return embeddedGuidance.MustKnob(face, verb, name)
}

// GuidanceRegistration is a verb's registration under a face's
// spelling — its purpose, help, long rendering, knobs, and the prose
// pointer — read at the face's construction with the same refusal
// (REQ-mcp-guidance).
func GuidanceRegistration(face, verb string) guidance.Registration {
	return embeddedGuidance.MustRegistration(face, verb)
}

// DescribeGuidanceSchema describes a served input schema's every
// property the walk reaches — a nested object's properties and an
// array's items — with the verb's knob of the property's own name
// under the mcp spelling, refusing a property the document does not
// knob; the wire face's coverage judgment enumerates the served
// schema the same way (REQ-mcp-guidance).
func DescribeGuidanceSchema(verb string, root guidance.SchemaNode) {
	embeddedGuidance.MustDescribeSchema("mcp", verb, root)
}
