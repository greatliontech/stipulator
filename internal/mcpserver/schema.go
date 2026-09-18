package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	guidancepkg "github.com/greatliontech/gofresh/guidance"
	stipulator "github.com/greatliontech/stipulator"
)

// The served schema machinery every registered tool shares.

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
