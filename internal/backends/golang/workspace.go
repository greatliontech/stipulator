package golang

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/gotool"
)

// resolveWorkspaceMode determines whether Go workspace mode is active for dir
// under mode and returns the resolved go.work file path when active.
func resolveWorkspaceMode(dir string, mode stipulatorv1.GoWorkspaceMode) (bool, string, error) {
	switch mode {
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_UNSPECIFIED,
		stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE:
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF:
		return false, "", nil
	default:
		return false, "", fmt.Errorf("workspace_mode %d is unsupported", mode)
	}
	work := filepath.Join(dir, "go.work")
	_, err := os.Stat(work)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, "", fmt.Errorf("reading go.work: %w", err)
	}
	hasWork := err == nil
	if mode == stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE && !hasWork {
		return false, "", fmt.Errorf("workspace_mode WORKSPACE but the tree declares no go.work")
	}
	if !hasWork {
		return false, "", nil
	}
	if workAbs, absErr := filepath.Abs(work); absErr == nil {
		work = workAbs
	}
	return true, work, nil
}

// workspaceMembers returns the tree's Go module directories, relative to
// dir: the go.work members when a workspace file is present, the root
// alone otherwise. Package patterns are module-scoped even in workspace
// mode, so every surface that walks "./..." — loading, witnessing — must
// iterate the members itself or nested modules silently vanish from
// verification.
func workspaceMembers(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.work"))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{"."}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading go.work: %w", err)
	}
	wf, err := modfile.ParseWork("go.work", b, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing go.work: %w", err)
	}
	root, err := resolvedTreeRoot(dir)
	if err != nil {
		return nil, err
	}
	var members []string
	for _, u := range wf.Use {
		clean := path.Clean(u.Path)
		if !hostPortableTreePath(u.Path) || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			// A member outside the tree would make the same commit verify
			// differently per machine: hermeticity is refused away, never
			// silently bent.
			return nil, fmt.Errorf("go.work member %q escapes the verification tree; members must lie within it", u.Path)
		}
		hostClean := filepath.FromSlash(clean)
		// The lexical check above refuses what the committed text says;
		// the resolved check refuses what the filesystem makes of it (an
		// in-tree symlink pointing out).
		if err := resolvedUnder(root, dir, hostClean); err != nil {
			return nil, fmt.Errorf("go.work member %q: %w", u.Path, err)
		}
		members = append(members, hostClean)
	}
	if len(members) == 0 {
		return nil, fmt.Errorf("go.work declares no members")
	}
	return members, nil
}

// resolvedTreeRoot resolves the verification tree root once for a batch
// of member checks.
func resolvedTreeRoot(dir string) (string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving verification tree root: %w", err)
	}
	return root, nil
}

// resolvedUnder refuses a tree-relative directory whose resolved
// location leaves the resolved tree root: a lexically in-tree path that
// is (or crosses) a symlink out of the tree would let verification
// operate outside the tree it claims to verify — hermeticity is refused
// away, never silently bent (REQ-go-workspace). An absent path resolves
// to nothing and passes: whatever later consumes it fails on its own
// terms, and nothing outside the tree was reached. The check binds
// validation time; a path re-pointed between validation and consumption
// is the same hold-still assumption the observation-coherence span
// already accepts for the run's reads.
func resolvedUnder(root, dir, rel string) error {
	p, err := filepath.EvalSymlinks(filepath.Join(dir, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolving %q: %w", rel, err)
	}
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return fmt.Errorf("%q resolves to %q, outside the verification tree; members must lie within it", rel, p)
	}
	return nil
}

// goworkEnv pins workspace mode for a spawned go command or package load:
// the tree's own go.work when it has one, explicitly off otherwise. The
// go command discovers workspace files by walking UP, so an enclosing
// repository's workspace would otherwise leak into fixture or corpus
// trees that are not its members and refuse their "./..." patterns.
func goworkEnv(dir string) ([]string, error) {
	return goworkModeEnv(dir, stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_UNSPECIFIED)
}

func goworkModeEnv(dir string, mode stipulatorv1.GoWorkspaceMode) ([]string, error) {
	workOn, work, err := resolveWorkspaceMode(dir, mode)
	if err != nil {
		return nil, err
	}
	pin := "off"
	if workOn {
		pin = work
	}
	// Normalized under gofresh's policy first (a malformed or duplicated
	// ambient entry refuses here, where the freshness engine this env
	// feeds would refuse it later); the pins below replace any ambient
	// GOWORK or GOPACKAGESDRIVER, never append beside one.
	env, err := gotool.NormalizeEnv(ambientEnviron())
	if err != nil {
		return nil, fmt.Errorf("inherited environment: %w", err)
	}
	// An ambient external package driver never shapes verification
	// (REQ-go-owned-processes), so the driver is pinned off: symbol
	// loading and toolchain queries always go through the real toolchain.
	// Policy normalization refuses a real ambient driver because there an
	// accepted, reviewed invocation record exists for the ambient control
	// to contradict; this environment backs no reviewed record, so the
	// pin — not a refusal — is the right shape here.
	env = gotool.SetEnv(env, "GOPACKAGESDRIVER", "off")
	env = gotool.SetEnv(env, "GOWORK", pin)
	// The toolchain's telemetry is owned at this root as at the policy
	// normalizer's (telemetry.go).
	return telemetryOffEnv(env)
}
