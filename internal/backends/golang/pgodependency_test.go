package golang

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestPGODefaultPresenceAndFailures(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	dir := t.TempDir()
	path := filepath.Join(dir, "default.pgo")
	main := []listedPackage{{ImportPath: "example.com/main", Name: "main", Dir: dir}}
	library := []listedPackage{{ImportPath: "example.com/library", Name: "library", Dir: dir}}
	read := func(pgo string, selected []listedPackage) []string {
		t.Helper()
		inputs, err := pgoBuildInputs(t.Context(), dir, pgo, selected)
		if err != nil {
			t.Fatal(err)
		}
		return inputs
	}
	absent := read("", main)
	if len(absent) != 1 || !strings.HasSuffix(absent[0], "=absent") {
		t.Fatalf("missing default has no absence identity: %v", absent)
	}
	if err := os.WriteFile(path, []byte("profile bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	present := read("", main)
	if slices.Equal(absent, present) {
		t.Fatal("creating a default profile kept the absence guard")
	}
	if !slices.Equal(present, read("auto", append(main, main...))) {
		t.Fatal("explicit auto or duplicate selection changed profile inputs")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if removed := read("", main); !slices.Equal(absent, removed) {
		t.Fatalf("removed default = %v, want absence %v", removed, absent)
	}
	if _, err := pgoBuildInputs(t.Context(), dir, "default.pgo", library); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("named missing profile = %v, want missing-file refusal", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, pgo := range []string{"", "auto", "default.pgo"} {
		if _, err := pgoBuildInputs(t.Context(), dir, pgo, main); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("directory profile with pgo %q: %v", pgo, err)
		}
	}
	for _, pgo := range []string{"", "auto"} {
		if inputs := read(pgo, library); len(inputs) != 0 {
			t.Fatalf("library consumed invalid default with pgo %q: %v", pgo, inputs)
		}
	}
	if inputs := read("off", main); len(inputs) != 0 {
		t.Fatalf("off consumed invalid default: %v", inputs)
	}
	if _, err := pgoBuildInputs(t.Context(), dir, "", []listedPackage{{ImportPath: "example.com/unknown", Dir: dir}}); err == nil {
		t.Fatal("unknown package name silently proved no default profile")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := pgoBuildInputs(ctx, dir, "", library); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled observation: %v", err)
	}
}

func TestPGORelevantReadErrorsRefuse(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	dir := writeModule(t, map[string]string{
		"main/default.pgo":       "profile",
		"unselected/default.pgo": "irrelevant",
	})
	mainDir := filepath.Join(dir, "main")
	path := filepath.Join(mainDir, "default.pgo")
	selected := []listedPackage{{ImportPath: "example.com/main", Name: "main", Dir: mainDir}}
	// No directory walk is needed: execute permission suffices for a named
	// profile read, while an unselected directory need not be accessible at all.
	if err := os.Chmod(mainDir, 0o111); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "unselected"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(mainDir, 0o700)
		_ = os.Chmod(filepath.Join(dir, "unselected"), 0o700)
	})
	if _, err := pgoBuildInputs(t.Context(), dir, "", selected); err != nil {
		t.Fatalf("named readable profile required directory listing: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("host can read permission-denied files")
	}
	for _, pgo := range []string{"", "auto", "main/default.pgo"} {
		if _, err := pgoBuildInputs(t.Context(), dir, pgo, selected); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("unreadable relevant profile with pgo %q: %v", pgo, err)
		}
	}
	if err := os.Chmod(mainDir, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := pgoBuildInputs(t.Context(), dir, "", selected); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("uncertain default absence did not refuse: %v", err)
	}
}

func TestPGODiscoveryUsesOriginalSelectedPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("lists selected packages in module and workspace contexts")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs", "REQ-go-build-selections")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":             "module example.com/root\n\ngo 1.27\n",
		"library.go":         "//go:build !command\n\npackage library\n",
		"main.go":            "//go:build command\n\npackage main\nfunc main() {}\n",
		"default.pgo":        "root profile",
		"nested/go.mod":      "module example.com/nested\n\ngo 1.27\n",
		"nested/main.go":     "package main\nfunc main() {}\n",
		"nested/default.pgo": "nested profile",
		"member/go.mod":      "module example.com/member\n\ngo 1.27\n",
		"member/main.go":     "package main\nfunc main() {}\n",
		"member/default.pgo": "member profile",
		"go.work":            "go 1.27\nuse (\n.\n./member\n)\n",
	})
	for _, tc := range []struct {
		name, root, pattern, packageName, profile string
		tags                                      []string
		workspace                                 bool
	}{
		{name: "library", pattern: ".", packageName: "library"},
		{name: "tagged-main", pattern: ".", packageName: "main", tags: []string{"command"}, profile: "default.pgo"},
		{name: "independent-nested", root: "nested", pattern: ".", packageName: "main", profile: "nested/default.pgo"},
		{name: "workspace-member", pattern: "example.com/member", packageName: "main", workspace: true, profile: "member/default.pgo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{tc.pattern})
			cfg.SetPlainWitness(true)
			cfg.SetModuleRoot(tc.root)
			cfg.SetTags(tc.tags)
			cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
			if tc.workspace {
				cfg.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE)
			}
			policy := &stipulatorv1.TestPolicy{}
			policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation(tc.name, cfg)})
			pc, err := CapturePolicy(t.Context(), dir, policy)
			if err != nil {
				t.Fatal(err)
			}
			discovery, err := pc.discover(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			n := pc.normalized[0]
			if len(n.selectedPackages) != 1 || n.selectedPackages[0].Name != tc.packageName {
				t.Fatalf("selected metadata = %+v", n.selectedPackages)
			}
			observation, err := observePGO(t.Context(), dir, discovery.groups[0])
			if err != nil {
				t.Fatal(err)
			}
			inputs := observation.inputs
			if tc.profile == "" {
				if len(inputs) != 0 {
					t.Fatalf("library selected a profile: %v", inputs)
				}
			} else if len(inputs) != 1 || !strings.Contains(inputs[0], `"`+tc.profile+`"=`) {
				t.Fatalf("inputs = %v, want only %s", inputs, tc.profile)
			}
		})
	}
}

func pgoTestProfile(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(deadline) {
	}
	pprof.StopCPUProfile()
	return buf.Bytes()
}

func TestPGODefaultMainProfileInvalidatesWarmWitness(t *testing.T) {
	if testing.Short() {
		t.Skip("executes plain witnesses with real PGO profiles")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-inputs")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":             "module example.com/pgomain\n\ngo 1.27\n",
		"main.go":            "package main\nfunc main() {}\nfunc F() int { return 1 }\n",
		"main_test.go":       "package main\nimport \"testing\"\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal(\"wrong value\") } }\n",
		"unselected/main.go": "package main\nfunc main() {}\n",
	})
	if err := os.Mkdir(filepath.Join(dir, "unselected/default.pgo"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	cfg.SetPlainWitness(true)
	cfg.SetAssumePure(true)
	policy := &stipulatorv1.TestPolicy{}
	policy.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("main", cfg)})
	writePolicyRecord(t, dir, policy)
	run := func(ran, fresh int) {
		t.Helper()
		got, err := RunWitnesses(t.Context(), dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Degraded != "" || got.Uncached != 0 || got.Ran != ran || got.Fresh != fresh || got.Outcomes["example.com/pgomain.TestF"] != verify.TestPassed {
			t.Fatalf("want ran=%d fresh=%d with passing cacheable witness; got %+v", ran, fresh, got)
		}
	}
	run(1, 0)
	run(0, 1)
	first, second := pgoTestProfile(t), pgoTestProfile(t)
	if bytes.Equal(first, second) {
		t.Fatal("profile fixture did not change bytes")
	}
	path := filepath.Join(dir, "default.pgo")
	for _, profile := range [][]byte{first, second} {
		if err := os.WriteFile(path, profile, 0o600); err != nil {
			t.Fatal(err)
		}
		run(1, 0)
		run(0, 1)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The store may retain the original absent-profile variant. It is valid
	// again after removal; either serving that variant or executing is sound.
	got, err := RunWitnesses(t.Context(), dir, noSeeding{})
	if err != nil || got.Degraded != "" || got.Uncached != 0 || got.Ran+got.Fresh != 1 || got.Outcomes["example.com/pgomain.TestF"] != verify.TestPassed {
		t.Fatalf("profile removal: %+v, %v", got, err)
	}
	run(0, 1)
}
