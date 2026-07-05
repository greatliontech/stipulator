package golang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
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

// RunTests executes the module's tests with -json and derives the test
// run: per-test outcomes plus runtime coverage registrations.
//
// This is the one sanctioned toolchain execution in the system: witnesses
// cannot exist without running tests, and there is no in-process way to do
// so (go/packages itself drives the go command under the hood). A non-zero
// exit with parsed events is data, not an error — failing tests appear as
// outcomes, and a test shadowed by a package abort (sibling panic, build
// failure) simply has no outcome, which the correlator reads as
// unwitnessed/broken. A run producing no events at all is an error.
func RunTests(dir string) (*verify.TestRun, error) {
	cmd := exec.Command("go", "test", "-json", "./...")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	type event struct {
		Action, Package, Test, Output string
	}
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}}
	dec := json.NewDecoder(&stdout)
	events := 0
	for dec.More() {
		var e event
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("parsing go test -json stream: %w", err)
		}
		events++
		if e.Test == "" {
			continue
		}
		key := e.Package + "." + e.Test
		switch e.Action {
		case "pass":
			tr.Outcomes[key] = verify.TestPassed
		case "fail":
			tr.Outcomes[key] = verify.TestFailed
		case "skip":
			tr.Outcomes[key] = verify.TestSkipped
		case "output":
			for _, m := range coversRe.FindAllStringSubmatch(e.Output, -1) {
				tr.Registrations = append(tr.Registrations, verify.Registration{
					Package: e.Package, Test: e.Test, Requirement: m[1],
				})
			}
		}
	}
	if events == 0 {
		if runErr != nil {
			return nil, fmt.Errorf("go test -json produced no events: %v: %s", runErr, stderr.String())
		}
		return nil, fmt.Errorf("go test -json produced no events")
	}
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
	return tr, nil
}
