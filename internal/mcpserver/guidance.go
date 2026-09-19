package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	guidancepkg "github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"
)

// guidanceDoc is the embedded guidance document; a malformed document
// is a build defect the parse-pinning test surfaces, so consumers
// fail loudly rather than serving nothing.
func guidanceDoc() *guidancepkg.Document { return stipulator.Guidance() }

func guidanceOrientation() string { return guidanceDoc().Orientation() }

// guidanceIn asks for one verb's section or, empty, the decision map.
type guidanceIn struct {
	Verb string `json:"verb,omitempty"`
}

// toolGuidance serves the embedded guidance document
// (REQ-mcp-guidance): a verb's full section under its mcp spelling,
// or the decision map for orientation.
func (s *Server) toolGuidance(ctx context.Context, req *mcp.CallToolRequest, in guidanceIn) (*mcp.CallToolResult, any, error) {
	if in.Verb == "" {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: guidanceDoc().Orientation()}}}, nil, nil
	}
	long, err := guidanceDoc().Long("mcp", in.Verb)
	if err != nil {
		return nil, nil, fmt.Errorf("%w; empty verb serves the decision map, which names every verb", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: long}}}, nil, nil
}
