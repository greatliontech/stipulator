package golang

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A //go:build race declaration resolves through a race invocation's
// view — the -race build sets the implicit race tag, so resolution
// spans exactly the selections execution builds — and execution
// discovery lists the same gated test the run would compile
// (REQ-go-build-selections' race dimension).
func TestRaceDimensionSpansResolutionAndDiscovery(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/raced\n\ngo 1.24\n",
		"p.go":   "package raced\n\nfunc Plain(x int) int { return x }\n",
		"race_guard.go": `//go:build race

package raced

import _ "example.com/raced/rdep"

func RaceOnly(x int) int { return x + 1 }
`,
		"rdep/rdep.go": "package rdep\n\nfunc init() {}\n",
		"race_guard_test.go": `//go:build race

package raced

import "testing"

func TestRaceOnly(t *testing.T) {
	if RaceOnly(1) != 2 {
		t.Fatal()
	}
}
`,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raceCfg := &stipulatorv1.GoInvocationConfig{}
	raceCfg.SetPackages([]string{"./..."})
	raceCfg.SetRace(true)
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("race", raceCfg)})
	writePolicyRecord(t, dir, p)

	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	res, hash, err := b.Resolve("example.com/raced.RaceOnly")
	if err != nil {
		t.Fatal(err)
	}
	if res != verify.Resolved || hash == "" {
		t.Fatalf("race-gated symbol did not resolve through the race view: %v", res)
	}

	n, err := NormalizeInvocation(context.Background(), dir, goInvocation("race", raceCfg))
	if err != nil {
		t.Fatal(err)
	}
	obligations, err := DiscoverInvocation(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range obligations {
		if o.Kind == ObligationTest && o.Name == "TestRaceOnly" {
			found = true
		}
	}
	if !found {
		t.Fatalf("discovery under the race invocation missed the race-gated test: %v", obligations)
	}
	// The closure listing runs under the same effective tag-set: a
	// dependency imported only by a race-gated file rides the package's
	// closure roots, so the observation bracket covers exactly what the
	// race build reads.
	closure := n.PkgClosureDirs["example.com/raced"]
	hasDep := false
	for _, rel := range closure {
		if rel == "rdep" {
			hasDep = true
		}
	}
	if !hasDep {
		t.Fatalf("race-gated dependency missing from the closure roots: %v", closure)
	}
}

// A cross-platform selection (declared GOOS/GOARCH off the host) gets
// no silent resolution view: a reference the loaded views cannot
// answer refuses with the unresolvable selection named, exactly as an
// unloadable view does (REQ-go-build-selections' platform clause).
func TestCrossPlatformSelectionRefusesByName(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/xplat\n\ngo 1.24\n",
		"p.go":   "package xplat\n\nfunc Plain(x int) int { return x }\n",
		"plan9_only.go": `//go:build plan9

package xplat

func PlanNineOnly(x int) int { return x }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	crossCfg := &stipulatorv1.GoInvocationConfig{}
	crossCfg.SetPackages([]string{"./..."})
	crossCfg.SetGoos("plan9")
	crossCfg.SetGoarch("arm")
	crossCfg.SetTags([]string{"xtag"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("cross", crossCfg)})
	writePolicyRecord(t, dir, p)

	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = b.Resolve("example.com/xplat.PlanNineOnly")
	if err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("cross-platform absence did not refuse with the view named: %v", err)
	}
	// The host view stays healthy: an ordinary symbol resolves.
	res, _, err := b.Resolve("example.com/xplat.Plain")
	if err != nil || res != verify.Resolved {
		t.Fatalf("host view degraded by the cross-platform refusal: %v, %v", res, err)
	}
}

// A declared GOOS/GOARCH equal to the host is the host view, never a
// refusal: the tagged symbol resolves through the invocation's view
// exactly as if the platform were undeclared
// (REQ-go-build-selections' platform clause).
func TestHostEqualPlatformDeclarationFoldsIntoHostView(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/hosteq\n\ngo 1.24\n",
		"p.go":   "package hosteq\n\nfunc Plain(x int) int { return x }\n",
		"tagged.go": `//go:build hosttag

package hosteq

func Tagged(x int) int { return x }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetGoos(runtime.GOOS)
	cfg.SetGoarch(runtime.GOARCH)
	cfg.SetTags([]string{"hosttag"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("hosteq", cfg)})
	writePolicyRecord(t, dir, p)

	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	res, hash, err := b.Resolve("example.com/hosteq.Tagged")
	if err != nil {
		t.Fatalf("host-equal declaration refused: %v", err)
	}
	if res != verify.Resolved || hash == "" {
		t.Fatalf("tagged symbol did not resolve through the host-equal view: %v", res)
	}
}

// Module mode, the PGO profile, and extra binary arguments partition
// capture groups: two invocations differing only there build or run
// two different things and must not share one analysis view, while
// Count is repetition of the same build and deliberately stays out
// (REQ-evidence-witness-freshness's capture-group partition).
func TestGroupKeySpansBuildDimensions(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	base := func() *NormalizedInvocation {
		return &NormalizedInvocation{Tags: []string{"a"}, Race: true}
	}
	for name, mutate := range map[string]func(*NormalizedInvocation){
		"module mode": func(n *NormalizedInvocation) { n.ModuleMode = stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR },
		"pgo":         func(n *NormalizedInvocation) { n.PGO = "default.pgo" },
		"args":        func(n *NormalizedInvocation) { n.Args = []string{"-myflag"} },
	} {
		changed := base()
		mutate(changed)
		if groupKey(base()) == groupKey(changed) {
			t.Errorf("%s delta did not partition the capture group", name)
		}
	}
	counted := base()
	counted.Count = 3
	if groupKey(base()) != groupKey(counted) {
		t.Error("count partitioned the capture group; repetition is not a build dimension")
	}
	// A joiner-byte argument value must not alias two argument entries
	// into one capture group.
	split := base()
	split.Args = []string{"-x", "-y"}
	joined := base()
	joined.Args = []string{"-x\x01-y"}
	if groupKey(split) == groupKey(joined) {
		t.Error("joiner byte aliased two argument entries into one capture group")
	}
}
