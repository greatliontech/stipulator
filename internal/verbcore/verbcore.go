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

// IDNoun names a requirement-identifier list in the one list grammar's
// refusals; ExcuseNoun a gap's excuse classes.
const (
	IDNoun     = "requirement identifiers"
	ExcuseNoun = "excuse classes"
)

// SplitList parses a comma list of a knob's values — the one list
// grammar both faces read for every comma-separated knob: blanks
// dropped, a JSON-array-encoded list tolerated (a client serializing
// the field as an array delivers it as one string), and a list that
// reduces to nothing refused naming the knob's noun — a list given but
// naming nothing is a malformed input, never the default or the whole
// corpus (REQ-check-preparation).
func SplitList(noun, commaList string) ([]string, error) {
	trimmed := strings.TrimSpace(commaList)
	if strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, fmt.Errorf("%s: the value looks like a JSON array but does not parse: %w", noun, err)
		}
		var vals []string
		for _, v := range arr {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("no %s given", noun)
		}
		return vals, nil
	}
	var vals []string
	for _, v := range strings.Split(commaList, ",") {
		if v = strings.TrimSpace(v); v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("no %s given", noun)
	}
	return vals, nil
}

// SplitListLoose is SplitList where an absent list (empty or blank)
// is no value at all — the knob's default — never an error.
func SplitListLoose(noun, commaList string) ([]string, error) {
	if strings.TrimSpace(commaList) == "" {
		return nil, nil
	}
	return SplitList(noun, commaList)
}

// SplitLists is SplitList over a repeated flag's values joined: no
// value given is no value at all; any value given parses under
// SplitList, so a repetition that reduces to nothing refuses like a
// single one.
func SplitLists(noun string, vals []string) ([]string, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	return SplitList(noun, strings.Join(vals, ","))
}

// SplitIDs is the one list grammar over requirement identifiers.
func SplitIDs(commaIDs string) ([]string, error) { return SplitList(IDNoun, commaIDs) }

// SplitIDsLoose is SplitIDs where an absent list (empty or blank) is
// no selection at all — the whole corpus — never an error.
func SplitIDsLoose(commaIDs string) ([]string, error) {
	return SplitListLoose(IDNoun, commaIDs)
}

// SplitIDLists is SplitIDs over a repeated flag's values joined.
func SplitIDLists(vals []string) ([]string, error) { return SplitLists(IDNoun, vals) }
