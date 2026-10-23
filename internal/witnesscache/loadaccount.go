package witnesscache

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

const refusalLimit = 25
const diagnosticLimit = 1024

// LoadResult separates admitted witness evidence from advisory load diagnostics.
// Account is nil for a complete load with no refusals, including a missing store.
type LoadResult struct {
	Records []Record
	Account *stipulatorv1.WitnessCacheLoadAccount
}

func (r *LoadResult) account() *stipulatorv1.WitnessCacheLoadAccount {
	if r.Account == nil {
		r.Account = &stipulatorv1.WitnessCacheLoadAccount{}
	}
	return r.Account
}

func (r *LoadResult) incomplete(operation string, err error) LoadResult {
	a := r.account()
	a.SetIncomplete(true)
	problem, truncated := boundedDiagnostic(fmt.Sprintf("%s: %v", operation, err))
	a.SetProblem(problem)
	a.SetProblemTruncated(truncated)
	r.Records = nil
	return *r
}

func (r *LoadResult) refuse(row *stipulatorv1.WitnessCacheRefusal) {
	a := r.account()
	if len(a.GetRefusals()) < refusalLimit {
		a.SetRefusals(append(a.GetRefusals(), row))
	} else if a.GetOmitted() < math.MaxUint64 {
		a.SetOmitted(a.GetOmitted() + 1)
	}
}

// boundedDiagnostic preserves valid UTF-8 and escapes invalid bytes without
// confusing a display spelling with the raw filename used for lookup.
func boundedDiagnostic(value string) (string, bool) {
	var b strings.Builder
	for len(value) > 0 {
		r, n := utf8.DecodeRuneInString(value)
		part := value[:n]
		if r == utf8.RuneError && n == 1 {
			part = strconv.QuoteToASCII(part)
			part = part[1 : len(part)-1]
		}
		if b.Len()+len(part) > diagnosticLimit {
			return b.String(), true
		}
		b.WriteString(part)
		value = value[n:]
	}
	return b.String(), false
}

func refusal(name string, cause stipulatorv1.WitnessCacheRefusalCause, detail string, rec *Record) *stipulatorv1.WitnessCacheRefusal {
	file, filenameTruncated := boundedDiagnostic(name)
	detail, detailTruncated := boundedDiagnostic(detail)
	row := &stipulatorv1.WitnessCacheRefusal{}
	row.SetFile(file)
	row.SetCause(cause)
	row.SetDetail(detail)
	row.SetFilenameTruncated(filenameTruncated)
	row.SetDetailTruncated(detailTruncated)
	if rec != nil {
		if len(rec.Group) > diagnosticLimit || len(rec.Package) > diagnosticLimit || len(rec.Test) > diagnosticLimit {
			row.SetCoordinateOmitted(true)
		} else {
			coordinate := &stipulatorv1.WitnessCacheCoordinate{}
			coordinate.SetGroup(rec.Group)
			coordinate.SetPackage(rec.Package)
			coordinate.SetTest(rec.Test)
			row.SetCoordinate(coordinate)
		}
	}
	return row
}

type recordReader interface {
	Read(string) ([]byte, error)
}

// scanRecords accounts only for this snapshot. The retention scan never calls
// it: a late file may protect a ledger without becoming an observed refusal.
func scanRecords(ctx context.Context, dir string, store recordReader, names []string, referenced map[string]bool) LoadResult {
	var result LoadResult
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result.incomplete("load witness records", err)
		}
		data, err := store.Read(name)
		if err != nil {
			result.refuse(refusal(name, stipulatorv1.WitnessCacheRefusalCause_WITNESS_CACHE_REFUSAL_CAUSE_READ, err.Error(), nil))
			continue
		}
		assessment := loadEntry(name, data, dir)
		if assessment.ledger != "" {
			referenced[assessment.ledger] = true
		}
		if assessment.record != nil {
			result.Records = append(result.Records, *assessment.record)
		} else {
			result.refuse(assessment.refusal)
		}
	}
	if err := ctx.Err(); err != nil {
		return result.incomplete("load witness records", err)
	}
	return result
}
