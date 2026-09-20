package mcpserver

import (
	"fmt"
	"strings"

	"github.com/greatliontech/stipulator/internal/verbcore"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/internal/verifyrun"
	"github.com/greatliontech/stipulator/internal/views"
)

// The input parsing the served verbs share.

// scopeFrom builds a scope from tool params, tolerating the same id
// encodings splitIDs does.
func scopeFrom(ids, bucket, filter, pathPrefix string) (views.Scope, error) {
	sc := views.Scope{Bucket: bucket, Filter: filter, Path: pathPrefix}
	if strings.TrimSpace(ids) != "" {
		parsed, err := verbcore.SplitIDs(ids)
		if err != nil {
			return views.Scope{}, err
		}
		sc.Ids = parsed
	}
	return sc, nil
}

// refuseProblems is this face's rendering of the one hygiene refusal
// (verifyrun.ProblemsError): every problem listed in one teaching
// refusal, the rendering every problem-refusing tool shares
// (REQ-check-preparation).
func refuseProblems(problems []verify.Problem) error {
	if verifyrun.RefuseProblems(problems) == nil {
		return nil
	}
	msgs := make([]string, 0, len(problems))
	for _, p := range problems {
		msgs = append(msgs, p.String())
	}
	return fmt.Errorf("verification problems:\n%s", strings.Join(msgs, "\n"))
}
