package golang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/greatliontech/stipulator/internal/profile"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// testArgs is the witness-run invocation: -race is always on, so every
// witness is race-attributed.
func testArgs() []string { return []string{"test", "-json", "-race", "-timeout=30m", "./..."} }

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
	members, err := workspaceMembers(dir)
	if err != nil {
		return nil, err
	}
	env := goworkEnv(dir)
	tr := &verify.TestRun{Outcomes: map[string]verify.TestOutcome{}, RaceEnabled: true}
	events := 0
	for _, m := range members {
		n, err := runMemberTests(filepath.Join(dir, m), env, tr)
		if err != nil {
			return nil, err
		}
		events += n
	}
	if events == 0 {
		return nil, fmt.Errorf("go test -json produced no events")
	}
	sortRegs(tr)
	return tr, nil
}

// runMemberTests executes one module's witness run, merging outcomes and
// registrations into tr; it returns the event count so a silent member is
// distinguishable from a silent workspace.
func runMemberTests(dir string, env []string, tr *verify.TestRun) (int, error) {
	cmd := exec.Command("go", testArgs()...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	type event struct {
		Action, Package, Test, Output string
	}
	dec := json.NewDecoder(&stdout)
	events := 0
	for dec.More() {
		var e event
		if err := dec.Decode(&e); err != nil {
			return 0, fmt.Errorf("parsing go test -json stream: %w", err)
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
	if events == 0 && runErr != nil {
		return 0, fmt.Errorf("go test -json in %s produced no events: %v: %s", dir, runErr, stderr.String())
	}
	return events, nil
}

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

// RunnableTests enumerates each package's top-level runnable tests — Test
// and Fuzz functions go test would execute, both variants folded to the
// plain import path — the expected witness set a freshness-aware run
// selects from (REQ-evidence-witness-freshness).
func (b *Backend) RunnableTests() map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, pkg := range b.pkgs {
		pkgPath := strings.TrimSuffix(pkg.PkgPath, "_test")
		for _, f := range pkg.Syntax {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || !runnableWitness(fd, pkg) {
					continue
				}
				key := pkgPath + "." + fd.Name.Name
				if seen[key] {
					continue
				}
				seen[key] = true
				out[pkgPath] = append(out[pkgPath], fd.Name.Name)
			}
		}
	}
	for _, names := range out {
		sort.Strings(names)
	}
	return out
}
