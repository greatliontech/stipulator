package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/views"
)

// The input parsing the served verbs share.

// scopeFrom builds a scope from tool params, tolerating the same id
// encodings splitIDs does.
func scopeFrom(ids, bucket, filter, pathPrefix string) (views.Scope, error) {
	sc := views.Scope{Bucket: bucket, Filter: filter, Path: pathPrefix}
	if strings.TrimSpace(ids) != "" {
		parsed, err := splitIDs(ids)
		if err != nil {
			return views.Scope{}, err
		}
		sc.Ids = parsed
	}
	return sc, nil
}

// verificationProblems folds a report's problems into one teaching
// refusal, every problem listed — the one rendering all three
// problem-refusing tools share.
func verificationProblems(rep *verify.Report) error {
	if len(rep.Problems) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(rep.Problems))
	for _, p := range rep.Problems {
		msgs = append(msgs, p.String())
	}
	return fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n"))
}

// splitIDsLoose splits a comma list; empty input is an empty selection,
// not an error.
func splitIDsLoose(commaIDs string) ([]string, error) {
	if strings.TrimSpace(commaIDs) == "" {
		return nil, nil
	}
	return splitIDs(commaIDs)
}

func splitIDs(commaIDs string) ([]string, error) {
	trimmed := strings.TrimSpace(commaIDs)
	// Tolerate a JSON-array-encoded list: clients that serialize the ids
	// field as an array deliver it as one string, and treating it as a
	// single identifier produces a mangled unknown-id error.
	if strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, fmt.Errorf("ids looks like a JSON array but does not parse: %w", err)
		}
		var ids []string
		for _, id := range arr {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("no requirement identifiers given")
		}
		return ids, nil
	}
	var ids []string
	for _, id := range strings.Split(commaIDs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no requirement identifiers given")
	}
	return ids, nil
}
