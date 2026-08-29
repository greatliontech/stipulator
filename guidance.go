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
	"sync"

	"github.com/greatliontech/gofresh/guidance"
)

//go:embed docs/guidance.md
var guidanceSrc []byte

var (
	guidanceOnce sync.Once
	guidanceDoc  *guidance.Document
	guidanceErr  error
)

// GuidanceDocument is the parsed embedded guidance source, parsed
// once; a malformed document is a build-time defect every consumer
// surfaces loudly.
func GuidanceDocument() (*guidance.Document, error) {
	guidanceOnce.Do(func() {
		guidanceDoc, guidanceErr = guidance.Parse(guidanceSrc)
	})
	return guidanceDoc, guidanceErr
}
