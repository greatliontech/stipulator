// Package verbcore holds what every verb core takes from a face: the
// corpus preparation, the policy capture, the backend set, and the
// witness run. A face builds one Deps and hands it to
// each core it renders; the cores compute and never write, and read
// the progress reporter the face put on the context (none on the CLI).
package verbcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/greatliontech/gofresh"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/check"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Deps is what a face supplies to a verb core.
type Deps struct {
	// Prepare compiles the corpus and loads the records (the face's
	// preparation, with its own diagnostics rendering).
	Prepare func() (*check.Prepared, error)
	// Capture loads the accepted policy for a witnessed operation.
	Capture func(context.Context) (*golang.Capture, error)
	// Backends builds the verification backends over the operation's
	// symbols; the core releases them.
	Backends func(context.Context, []string) (map[string]verify.Backend, error)
	// RunTests executes the witness run — the whole policy when scope
	// is nil, the scoped selection otherwise; why names a scope for a
	// face that announces it.
	RunTests func(ctx context.Context, pc *golang.Capture, seeding verify.WitnessSeeding, scope map[gofresh.Subject]bool, why string) (*verify.TestRun, error)
}

// SplitIDs parses a comma list of requirement identifiers — the one
// grammar both faces read: blanks dropped, a JSON-array-encoded list
// tolerated (a client serializing the ids field as an array delivers
// it as one string), and a list that reduces to nothing refused — an
// identifier list given but naming no requirement is a malformed
// input, never the whole corpus (REQ-check-preparation).
func SplitIDs(commaIDs string) ([]string, error) {
	trimmed := strings.TrimSpace(commaIDs)
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

// SplitIDsLoose is SplitIDs where an absent list (empty or blank) is
// no selection at all — the whole corpus — never an error.
func SplitIDsLoose(commaIDs string) ([]string, error) {
	if strings.TrimSpace(commaIDs) == "" {
		return nil, nil
	}
	return SplitIDs(commaIDs)
}

// SplitIDLists is SplitIDs over a repeated flag's values joined: no
// value given is no selection; any value given parses under SplitIDs,
// so a repetition that reduces to nothing refuses like a single one.
func SplitIDLists(vals []string) ([]string, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	return SplitIDs(strings.Join(vals, ","))
}
