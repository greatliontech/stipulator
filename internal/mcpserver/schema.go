package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"
)

// The served schema machinery every registered tool shares.

// knobbedTool is a served tool whose input schema's every property
// description, at every depth — a nested object's properties and an
// array item's properties alike (the batch authoring form's claims) —
// is gofresh's schema rendering of the guidance document's knob under
// the tool's mcp spelling, never a second literal, and whose
// description is the registration's purpose; a property the document
// does not knob refuses at construction, so the served set cannot
// outgrow the document silently (REQ-mcp-guidance).
func knobbedTool[In any](verb string) *mcp.Tool {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic("mcpserver: input schema for " + verb + ": " + err.Error())
	}
	stipulator.DescribeGuidanceSchema(verb, schemaNode{schema})
	return &mcp.Tool{Name: verb, Description: stipulator.GuidanceRegistration("mcp", verb).Description, InputSchema: schema}
}

// schemaNode adapts a JSON schema to the walk gofresh's guidance
// package owns: an object's property names and nodes, an array's item
// schema, the description setter — the two-value answers keep a nil
// schema pointer out of the interface.
type schemaNode struct{ s *jsonschema.Schema }

func (n schemaNode) Properties() []string {
	names := make([]string, 0, len(n.s.Properties))
	for name := range n.s.Properties {
		names = append(names, name)
	}
	return names
}

func (n schemaNode) Property(name string) (guidance.SchemaNode, bool) {
	p, ok := n.s.Properties[name]
	if !ok || p == nil {
		return nil, false
	}
	return schemaNode{p}, true
}

func (n schemaNode) Items() (guidance.SchemaNode, bool) {
	if n.s.Items == nil {
		return nil, false
	}
	return schemaNode{n.s.Items}, true
}

func (n schemaNode) Describe(text string) { n.s.Description = text }
