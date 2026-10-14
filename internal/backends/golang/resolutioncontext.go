package golang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/greatliontech/gofresh/gotool"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func (s buildSelection) directory(root string) string {
	return filepath.Join(root, filepath.FromSlash(s.moduleRoot))
}

// members names the modules visible in a resolution view. Workspace-off
// declarations own their module alone; workspace views retain the declared
// workspace membership rather than borrowing an enclosing workspace.
func (s buildSelection) members(root string) ([]string, error) {
	if s.workspaceMode == stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF {
		if s.moduleRoot == "" {
			return []string{"."}, nil
		}
		return []string{filepath.FromSlash(s.moduleRoot)}, nil
	}
	_, err := os.Stat(filepath.Join(root, "go.work"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading resolution workspace: %w", err)
	}
	hasWork := err == nil
	if s.workspaceMode == stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE && !hasWork {
		return nil, fmt.Errorf("resolution workspace_mode WORKSPACE but the tree declares no go.work")
	}
	if hasWork {
		return workspaceMembers(root)
	}
	if s.moduleRoot == "" {
		return []string{"."}, nil
	}
	return []string{filepath.FromSlash(s.moduleRoot)}, nil
}

func workspaceWord(mode stipulatorv1.GoWorkspaceMode) string {
	switch mode {
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF:
		return "off"
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE:
		return "workspace"
	}
	return ""
}

func resolutionMembers(root string, sels []buildSelection) ([]string, error) {
	seen := map[string]bool{}
	for _, sel := range sels {
		members, err := sel.members(root)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			seen[member] = true
		}
	}
	out := make([]string, 0, len(seen))
	for member := range seen {
		out = append(out, member)
	}
	sort.Strings(out)
	return out, nil
}

// selectionModules restricts ownership to the configurations this view
// actually holds. Identical module paths in independent roots cannot borrow
// one another's helper-loading configuration.
func (b *Backend) selectionModules(sel string) map[string]string {
	out := map[string]string{}
	for member, path := range b.modules {
		if b.lazyCfg[member][sel] != nil {
			out[member] = path
		}
	}
	return out
}

func selectionWorkspace(env []string, mode stipulatorv1.GoWorkspaceMode) []string {
	if mode == stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF {
		return gotool.SetEnv(env, "GOWORK", "off")
	}
	return env
}
