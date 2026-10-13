package witnesscache

import (
	"bytes"
	"encoding/json"
	"io"
)

// envelopeFields reads one whole object and judges every field occurrence:
// no member is nullable, including case aliases the typed decoder recognizes.
// A later value cannot hide an earlier null. Syntactically complete fields
// remain available after a null refusal solely to retain the record's ledger
// reference; they confer no record admission. Typed and native codecs still
// own member shapes. As with encoding/json, the map holds the last occurrence.
func envelopeFields(data []byte) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	nonNull := true
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return nil, false
		}
		if isJSONNull(value) {
			nonNull = false
		}
		// Token inside an object's member list returns a string key or error.
		fields[key.(string)] = value
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') || dec.Decode(&struct{}{}) != io.EOF {
		return nil, false
	}
	return fields, nonNull
}
