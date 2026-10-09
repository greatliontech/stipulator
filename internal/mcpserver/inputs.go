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
	return refuseProblemsAccounted(problems, nil)
}

// refuseProblemsAccounted is refuseProblems carrying the serving path's
// account the refused pass read after its publishing close — the digest's
// rule, the per-symbol typed lines left out — so a refusal never hides what
// the close published (REQ-evidence-resolution-freshness-account).
func refuseProblemsAccounted(problems []verify.Problem, notices []string) error {
	if verifyrun.RefuseProblems(problems) == nil {
		return nil
	}
	msgs := make([]string, 0, len(problems))
	for _, p := range problems {
		msgs = append(msgs, p.String())
	}
	return fmt.Errorf("%s", resolutionDigest("verification problems:\n"+strings.Join(msgs, "\n"), notices))
}

// refuseReport is refuseProblems over a verification report, the
// report's account carried.
func refuseReport(rep *verify.Report) error {
	return refuseProblemsAccounted(rep.Problems, rep.ResolutionNotices)
}
