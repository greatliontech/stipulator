// Package wire renders report messages for machine consumers. One
// projection — the ProtoJSON encoding of the message — feeds both the
// CLI's --json output and the MCP structured tool results, so the two
// surfaces render the same facts by construction and cannot drift.
package wire

import (
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// CanonicalJSON renders a message's deterministic JSON projection: the
// ProtoJSON encoding re-serialized with sorted keys, fixed indentation,
// and a trailing newline, because protojson.Marshal deliberately
// randomizes its whitespace while machine consumers pin bytes.
func CanonicalJSON(m proto.Message) ([]byte, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// StructuredContent renders the same ProtoJSON projection as the generic
// map an MCP structured tool result carries.
func StructuredContent(m proto.Message) (map[string]any, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
