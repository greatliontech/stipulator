package golang

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestNestedAnalysisAndExecutionUseTheTreeProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a nested analysis view with a real PGO profile")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	dir := independentModulePolicy(t)
	profile := pgoTestProfile(t)
	path := filepath.Join(dir, "profile.pgo")
	if err := os.WriteFile(path, profile, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetModuleRoot("consumer")
	cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
	cfg.SetPlainWitness(true)
	cfg.SetPgo("profile.pgo")
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("consumer", cfg)})
	pc, err := CapturePolicy(t.Context(), dir, policy)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := pc.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.groups) != 1 {
		t.Fatalf("groups: %d", len(discovery.groups))
	}
	group := discovery.groups[0]
	snapshot, _, err := groupSnapshot(t.Context(), dir, group)
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.BindRuntime(t.Context(), gofresh.RuntimeDescription{Directory: group.directory(dir), Environment: group.witnessEnv})
	if err != nil {
		t.Fatal(err)
	}
	actual := map[gofresh.Subject]gofresh.Fingerprint{}
	for _, subject := range groupSubjects(group) {
		fp, err := view.CurrentFacts(t.Context(), subject)
		if err != nil {
			t.Fatal(err)
		}
		actual[subject] = fp
	}
	// Compare analysis with the literal flag that addresses the fixture
	// actually written at the corpus root, not with the group's flag builder.
	inputs, err := pgoBuildInputs(t.Context(), dir, "profile.pgo", group.selectedPackages)
	if err != nil {
		t.Fatal(err)
	}
	control, err := gofresh.NewSession(t.Context(), gofresh.BuildDescription{Directory: filepath.Join(dir, "consumer"), Environment: group.witnessEnv, Flags: []string{"-pgo=" + path}, Inputs: append([]string{}, inputs...)}, gofresh.SessionPolicy{EvidenceRoot: dir, Execution: gofresh.PackageExecution, Runner: engineRunner})
	if err != nil {
		t.Fatal(err)
	}
	controlSnapshot, err := control.Snapshot(t.Context(), groupSubjects(group), gofresh.CodeResult)
	if err != nil {
		t.Fatal(err)
	}
	controlView, err := controlSnapshot.BindRuntime(t.Context(), gofresh.RuntimeDescription{Directory: group.directory(dir), Environment: group.witnessEnv})
	if err != nil {
		t.Fatal(err)
	}
	for subject, fp := range actual {
		want, err := controlView.CurrentFacts(t.Context(), subject)
		if err != nil {
			t.Fatal(err)
		}
		if fp.Guards.BuildConfig != want.Guards.BuildConfig {
			t.Fatalf("analysis did not use the tree-rooted profile for %v", subject)
		}
	}
	args := retainedBuildArgs(pc.normalized[0], []string{"example.net/independent"}, "/retained-package.test", false)
	if !slices.Contains(args, "-pgo="+path) {
		t.Fatalf("execution profile is not corpus-rooted: %v", args)
	}
}
