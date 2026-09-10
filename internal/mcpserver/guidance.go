package mcpserver

import (
	guidancepkg "github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"

	"context"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// knobbedTool is a served tool whose input schema's property
// descriptions are the guidance document's knob text — each knob's
// terse first clause (stipulator.KnobClause) — rendered at
// registration, never a second literal beside the document, at every
// depth of the schema: a nested object's properties (the batch
// authoring form's claims) take the same verb's knobs by name. A
// property the document does not knob is a build defect
// (REQ-mcp-guidance).
func knobbedTool[In any](verb string) *mcp.Tool {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic("mcpserver: input schema for " + verb + ": " + err.Error())
	}
	knobSchema(guidanceDoc(), verb, schema)
	return &mcp.Tool{Name: verb, Description: guidanceDescription(verb), InputSchema: schema}
}

// knobSchema renders every property description under schema, into
// arrays and nested objects.
func knobSchema(doc *guidancepkg.Document, verb string, schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	for name, prop := range schema.Properties {
		k, err := doc.Knob("mcp", verb, name)
		if err != nil {
			panic("mcpserver: " + err.Error())
		}
		prop.Description = stipulator.KnobClause(k.Text)
		knobSchema(doc, verb, prop)
	}
	knobSchema(doc, verb, schema.Items)
}

// guidanceDescription is a tool's one-line purpose, served from the
// guidance document under the tool's mcp spelling
// (REQ-mcp-guidance).
func guidanceDescription(verb string) string {
	d, err := guidanceDoc().Description("mcp", verb)
	if err != nil {
		panic("mcpserver: " + err.Error())
	}
	return d
}

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
