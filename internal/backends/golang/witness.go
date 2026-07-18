package golang

import (
	"regexp"
	"slices"
	"sort"

	"github.com/greatliontech/stipulator/internal/profile"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// coversRe extracts requirement identifiers from stipulate registration
// lines in test output. It is built from stipulate.Marker so the helper
// and the correlator cannot drift apart.
var coversRe = regexp.MustCompile(regexp.QuoteMeta(stipulate.Marker) + `(` + profile.IDPattern + `)\b`)

// sortRegs orders a run's registrations deterministically and compacts
// duplicates: one claim per (package, test, requirement) triple however
// many times the marker line was printed.
func sortRegs(tr *verify.TestRun) {
	sort.Slice(tr.Registrations, func(i, j int) bool {
		a, b := tr.Registrations[i], tr.Registrations[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Test != b.Test {
			return a.Test < b.Test
		}
		return a.Requirement < b.Requirement
	})
	tr.Registrations = slices.Compact(tr.Registrations)
}
