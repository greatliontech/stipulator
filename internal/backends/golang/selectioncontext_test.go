package golang

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func selectedContextForTest(t *testing.T, dir string, sel buildSelection) *selectedContext {
	t.Helper()
	view, err := prepareSelectedContext(dir, sel)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

// Count the configuration input read, not incidental native subprocesses.
// Two genuinely different selections must each prepare once within an endpoint,
// while verifyInventoryUnchanged performs fresh inventory queries on every call.
func TestSelectionEndpointPreparesOnceAcrossConsumers(t *testing.T) {
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.com/prepared\n\ngo 1.27\n",
		"plain.go": "//go:build !dst\n\npackage prepared\nfunc F() {}\n",
		"dst.go":   "//go:build dst\n\npackage prepared\nfunc F(int) {}\n",
	})
	dstPolicy(t, dir)
	s, err := newServed(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	s.sels, _, err = policyBuildSelections(dir)
	if err != nil || len(s.sels) != 2 {
		t.Fatalf("selections: %v %v", s.sels, err)
	}
	const symbol = "example.com/prepared.F"
	symbols := []string{symbol}
	s.set = map[string]bool{symbol: true}
	oldAmbient, oldHook := ambientEnviron, commandHook
	t.Cleanup(func() { ambientEnviron, commandHook = oldAmbient, oldHook })
	preparations, inventories := 0, 0
	ambientEnviron = func() []string { preparations++; return oldAmbient() }
	commandHook = func(name string, args []string) {
		if name == "go" && slices.Contains(args, "-json=ImportPath,Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,InvalidGoFiles,Error") {
			inventories++
		}
	}
	endpoint := newSelectionEndpoint(s.ctx, s.dir, s.sels)
	if err := s.verifyInventoryUnchanged(endpoint, "default", nil); err != nil || preparations != 0 || inventories != 0 {
		t.Fatalf("empty inventory check observed inputs: %v preparations=%d inventories=%d", err, preparations, inventories)
	}
	s.inventoryPackages(endpoint, symbols)
	if !s.inventoryComplete() {
		t.Fatal(s.Notices())
	}
	var captures []gofresh.Fingerprint
	for _, sel := range s.sels {
		cap, ok := s.captureSourceView(endpoint, sel.key(), symbols, "opening")
		if !ok || len(cap.present) != 1 {
			t.Fatalf("capture under %q: %+v; %v", sel.key(), cap, s.Notices())
		}
		captures = append(captures, cap.present[gofresh.Subject{Package: "example.com/prepared", Symbol: "F"}])
	}
	if closureMoved(captures[0], captures[1]) == "" {
		t.Fatal("positive control lost the distinct selected declarations")
	}
	before := inventories
	for _, sel := range s.sels {
		if err := s.verifyInventoryUnchanged(endpoint, sel.key(), symbols); err != nil {
			t.Fatalf("verifyInventoryUnchanged(%q): %v", sel.key(), err)
		}
	}
	if inventories-before != len(s.sels) {
		t.Fatalf("inventory check reused stale observations: before=%d after=%d", before, inventories)
	}
	if preparations != len(s.sels) {
		t.Fatalf("configuration assembled %d times for %d selections", preparations, len(s.sels))
	}
	closing := newSelectionEndpoint(s.ctx, s.dir, s.sels)
	for _, sel := range s.sels {
		if err := s.verifyInventoryUnchanged(closing, sel.key(), symbols); err != nil {
			t.Fatal(err)
		}
	}
	if preparations != 2*len(s.sels) {
		t.Fatalf("new endpoint did not prepare independently: %d", preparations)
	}
	// The default declaration now disappears. A fresh inventory check must
	// refuse against the original boundary without mutating s.inventory.
	if err := os.WriteFile(filepath.Join(dir, "plain.go"), []byte("//go:build absent\n\npackage prepared\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyInventoryUnchanged(endpoint, "default", symbols); !errors.Is(err, errInventoryChanged) {
		t.Fatalf("changed inventory accepted: %v", err)
	}
	if !s.inventory["default"]["example.com/prepared"] {
		t.Fatal("a current inventory replaced the initial declaration boundary")
	}
}

func TestSelectedContextPreservesConsumerViews(t *testing.T) {
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.work":       "go 1.27\nuse (\n ./a\n ./helper\n)\n",
		"a/go.mod":      "module example.com/shared\n\ngo 1.27\n",
		"a/p.go":        "//go:build chosen\n\npackage shared\nfunc F() int { return 1 }\n",
		"b/go.mod":      "module example.com/shared\n\ngo 1.27\n",
		"b/p.go":        "//go:build chosen\n\npackage shared\nfunc F() int { return 2 }\n",
		"helper/go.mod": "module example.com/helper\n\ngo 1.27\n",
		"helper/p.go":   "package helper\nfunc H() {}\n",
	})
	selections := []buildSelection{
		{tags: []string{"chosen"}, toolchain: "local", moduleRoot: "a", workspaceMode: stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF, moduleMode: stipulatorv1.GoModuleMode_GO_MODULE_MODE_READONLY},
		{tags: []string{"chosen"}, toolchain: "local", moduleRoot: "b", workspaceMode: stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF, moduleMode: stipulatorv1.GoModuleMode_GO_MODULE_MODE_MOD},
		{tags: []string{"chosen"}, toolchain: "local", moduleRoot: "a", workspaceMode: stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE},
	}
	endpoint := newSelectionEndpoint(t.Context(), dir, selections)
	var independent []string
	for _, sel := range selections {
		t.Run(sel.key(), func(t *testing.T) {
			view, err := endpoint.selected(sel.key())
			if err != nil {
				t.Fatal(err)
			}
			wantDir := filepath.Join(dir, sel.moduleRoot)
			wantWork := "off"
			if sel.workspaceMode == stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE {
				wantWork = filepath.Join(dir, "go.work")
			}
			phase := "inventory"
			seen := map[string]int{}
			inventoryDirs := map[string]bool{}
			observe := func(cmd *exec.Cmd) {
				if len(cmd.Args) < 2 || cmd.Args[1] != "list" {
					return
				}
				seen[phase]++
				if phase == "inventory" {
					inventoryDirs[cmd.Dir] = true
				}
				memberQuery := phase == "inventory" && wantWork != "off" && cmd.Dir == filepath.Join(dir, "helper")
				if cmd.Dir != wantDir && !memberQuery {
					t.Errorf("%s directory=%q, want %q", phase, cmd.Dir, wantDir)
				}
				for _, pin := range []string{"GOWORK=" + wantWork, "GOTOOLCHAIN=local", "GOPACKAGESDRIVER=off", "GOENV=off"} {
					if !slices.Contains(cmd.Env, pin) {
						t.Errorf("%s lacks environment pin %s", phase, pin)
					}
				}
				for _, flag := range selectionViewFlags(sel) {
					if !slices.Contains(cmd.Args, flag) {
						t.Errorf("%s lacks flag %s: %v", phase, flag, cmd.Args)
					}
				}
			}
			oldRunner, oldEngine := ownedRunner, engineCommandHook
			t.Cleanup(func() { ownedRunner, engineCommandHook = oldRunner, oldEngine })
			ownedRunner.Prepare = observe
			engineCommandHook = observe
			symbols := []string{"example.com/shared.F"}
			if wantWork != "off" {
				symbols = append(symbols, "example.com/helper.H")
			}
			inventory, err := declarationPackages(t.Context(), view, symbols)
			if err != nil || !inventory["example.com/shared"] {
				t.Fatalf("inventory: %v %v", inventory, err)
			}
			if wantWork != "off" && (!inventory["example.com/helper"] || !inventoryDirs[filepath.Join(dir, "helper")]) {
				t.Fatalf("workspace inventory missed its owning member: %v %v", inventory, inventoryDirs)
			}
			phase = "source"
			engine, err := endpoint.session(view, newToolchainSample())
			if err != nil {
				t.Fatal(err)
			}
			subjects := []gofresh.Subject{{Package: "example.com/shared", Symbol: "F"}}
			if wantWork != "off" {
				subjects = append(subjects, gofresh.Subject{Package: "example.com/helper", Symbol: "H"})
			}
			snap, err := engine.Snapshot(t.Context(), subjects, gofresh.SourceResult)
			if err != nil {
				t.Fatal(err)
			}
			fps, err := snap.CaptureBatch(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, consumer := range []string{"inventory", "source"} {
				if seen[consumer] == 0 {
					t.Errorf("%s command path was not exercised", consumer)
				}
			}
			if wantWork == "off" {
				independent = append(independent, fps[gofresh.Subject{Package: "example.com/shared", Symbol: "F"}].MaximalClosure)
			} else if view.modules["helper"] != "example.com/helper" {
				t.Fatalf("workspace member ownership lost: %v", view.modules)
			}
		})
	}
	if len(independent) != 2 || independent[0] == independent[1] {
		t.Fatalf("equal import paths in independent roots shared a closure: %v", independent)
	}
}

func TestSelectionEndpointOwnsConfigurationAndFailure(t *testing.T) {
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/owned\n\ngo 1.27\n"})
	sel := buildSelection{tags: []string{"chosen"}}
	key := sel.key()
	endpoint := newSelectionEndpoint(t.Context(), dir, []buildSelection{sel, {}})
	sel.tags[0] = "changed"
	view, err := endpoint.selected(key)
	if err != nil || !slices.Contains(view.flags, "-tags=chosen") {
		t.Fatalf("endpoint borrowed selection storage: %v %v", view, err)
	}
	oldAmbient := ambientEnviron
	t.Cleanup(func() { ambientEnviron = oldAmbient })
	reads := 0
	ambientEnviron = func() []string { reads++; return []string{"malformed"} }
	_, first := endpoint.selected("default")
	_, second := endpoint.selected("default")
	if first == nil || first != second || reads != 1 || !strings.Contains(first.Error(), "environment") {
		t.Fatalf("failed preparation was retried or lost: %v %v reads=%d", first, second, reads)
	}
	if got, err := endpoint.selected(key); err != nil || got != view {
		t.Fatalf("one selection failure discarded another's preparation: %v %v", got, err)
	}
}
