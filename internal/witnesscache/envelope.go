package witnesscache

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

// envelopeFields reads one whole object and judges every field occurrence:
// no member is nullable, duplicated, unknown or a case alias accepted by the
// typed decoder. Syntactically complete fields
// remain available after refusal solely to retain the record's ledger
// reference; they confer no record admission. Typed and native codecs still
// own member shapes. As with encoding/json, the map holds the last occurrence.
func envelopeFields(data []byte) (map[string]json.RawMessage, bool) {
	return objectFields(data, "version", "group", "package", "test", "fingerprint", "outcomes", "registrations", "observationExclusions", "observationNamespaces")
}

// objectFields validates member occurrences before typed decoding can collapse
// duplicates or turn null into a zero value. With names, only their exact
// spellings are allowed; without names the object is a string-keyed map.
// A syntactically complete refused object still supplies its last-value field
// map for weak ledger retention. Native fingerprint members never enter here.
func objectFields(data []byte, names ...string) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	canonical := true
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return nil, false
		}
		name := key.(string)
		if _, duplicate := fields[name]; duplicate || isJSONNull(value) {
			canonical = false
		}
		if len(names) != 0 && !slices.Contains(names, name) {
			canonical = false
		}
		// Token inside an object's member list returns a string key or error.
		fields[name] = value
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') || dec.Decode(&struct{}{}) != io.EOF {
		return nil, false
	}
	return fields, canonical
}

// canonicalCollections preserves occurrence information in the envelope's
// collections. Typed decoding still owns value types; native decoding owns the
// fingerprint. Empty arrays remain valid. A namespace row carries both of its
// string members, including when their explicitly recorded values are empty.
func canonicalCollections(fields map[string]json.RawMessage) bool {
	if _, ok := objectFields(fields["outcomes"]); !ok {
		return false
	}
	for _, name := range []string{"registrations", "observationNamespaces", "observationExclusions"} {
		data, present := fields[name]
		if !present {
			continue
		}
		var elements []json.RawMessage
		if json.Unmarshal(data, &elements) != nil {
			return false
		}
		for _, element := range elements {
			if isJSONNull(element) {
				return false
			}
			switch name {
			case "registrations":
				if _, ok := objectFields(element, "Package", "Test", "Requirement"); !ok {
					return false
				}
			case "observationNamespaces":
				// Unique members restricted to these two names establish both
				// required fields exactly when two members are present.
				if members, ok := objectFields(element, "dir", "pattern"); !ok || len(members) != 2 {
					return false
				}
			}
		}
	}
	return true
}

// strictlyOrdered validates a recorded set without sorting or deduplicating
// historical evidence. Empty and absent slices both denote an empty set.
func strictlyOrdered[T any](rows []T, compare func(T, T) int) bool {
	for i := 1; i < len(rows); i++ {
		if compare(rows[i-1], rows[i]) >= 0 {
			return false
		}
	}
	return true
}
