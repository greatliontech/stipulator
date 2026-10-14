package golang

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"testing"
	"time"

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
	var profile bytes.Buffer
	if err := pprof.StartCPUProfile(&profile); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(until) {
	}
	pprof.StopCPUProfile()
	path := filepath.Join(dir, "profile.pgo")
	if err := os.WriteFile(path, profile.Bytes(), 0o644); err != nil {
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
	engine, err := groupEngine(t.Context(), dir, group)
	if err != nil {
		t.Fatal(err)
	}
	view, err := engine.NewView(t.Context(), groupSubjects(group), group.directory(dir))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := view.CaptureBatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Compare analysis with the literal flag that addresses the fixture
	// actually written at the corpus root, not with the group's flag builder.
	inputs, err := pgoBuildInputs(dir, "consumer", "profile.pgo")
	if err != nil {
		t.Fatal(err)
	}
	control, err := newEngine(t.Context(), filepath.Join(dir, "consumer"), group.env, []string{"-pgo=" + path}, gofresh.WithBuildInputs(inputs...), gofresh.WithEvidenceRoot(dir))
	if err != nil {
		t.Fatal(err)
	}
	controlView, err := control.NewView(t.Context(), groupSubjects(group), filepath.Join(dir, "consumer"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := controlView.CaptureBatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for subject, fp := range actual {
		if fp.Guards.BuildConfig != want[subject].Guards.BuildConfig {
			t.Fatalf("analysis did not use the tree-rooted profile for %v", subject)
		}
	}
	args := testCommandArgs(pc.normalized[0], "example.net/independent", nil, "")
	if !slices.Contains(args, "-pgo="+path) {
		t.Fatalf("execution profile is not corpus-rooted: %v", args)
	}
}
