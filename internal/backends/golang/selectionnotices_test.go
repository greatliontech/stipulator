package golang

import (
	"context"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/closure"
	"github.com/greatliontech/stipulator/stipulate"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// A policy invocation declaring build tags runs under a toolchain
// selection the freshness engine's two-axis audit fail-closes until the
// selection's standard-library delta is walked: observation admissions
// strip and serving degrades to execution. The policy tier names that
// cost where the tags were declared — the notice carries the invocation
// name and the engine's own rendering — while walked selections and
// non-witness invocations (which build no engine) raise nothing
// (REQ-check-policy-notices).
//
// Deliberately not //gofresh:pure: normalization shells the go toolchain.
func TestSelectionNoticesAttributeUnwalkedSelections(t *testing.T) {
	stipulate.Covers(t, "REQ-check-policy-notices")
	if !closure.AuditedToolchainSelection(nil, "", "") {
		t.Skip("running toolchain not in the walked audit list; the version canary covers this")
	}
	neutralAmbient(t)
	dir := discoverFixture(t)
	tagged := &stipulatorv1.GoInvocationConfig{}
	tagged.SetPackages([]string{"./..."})
	tagged.SetRace(true)
	tagged.SetTags([]string{"dup"})
	vanilla := &stipulatorv1.GoInvocationConfig{}
	vanilla.SetPackages([]string{"./..."})
	vanilla.SetRace(true)
	plainTagged := &stipulatorv1.GoInvocationConfig{}
	plainTagged.SetPackages([]string{"./..."})
	plainTagged.SetTags([]string{"dup"})
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("plain-tagged", plainTagged),
		goInvocation("tagged", tagged),
		goInvocation("vanilla", vanilla),
	})
	got := SelectionNotices(context.Background(), dir, pol)
	if len(got) != 1 {
		t.Fatalf("notices = %v, want exactly the witness-eligible tagged invocation's", got)
	}
	for _, frag := range []string{`invocation "tagged"`, `selection "dup,race"`, "unwalked", "observation admissions are disabled"} {
		if !strings.Contains(got[0], frag) {
			t.Fatalf("notice %q missing %q", got[0], frag)
		}
	}
}
