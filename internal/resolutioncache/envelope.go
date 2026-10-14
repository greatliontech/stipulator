package resolutioncache

import (
	"bytes"
	"encoding/json"
	"io"
)

// objectFields preserves native members as raw bytes while refusing aliases,
// duplicates, nulls, unknown keys and trailing input at this envelope's level.
func objectFields(data []byte, names ...string) (map[string]json.RawMessage, bool) {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, false
		}
		name := key.(string)
		if !allowed[name] || fields[name] != nil {
			return nil, false
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, false
		}
		fields[name] = raw
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') || dec.Decode(&struct{}{}) != io.EOF {
		return nil, false
	}
	return fields, true
}
