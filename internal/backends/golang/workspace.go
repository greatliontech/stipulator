package golang

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/greatliontech/gofresh/gotool"
)

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
		clean := filepath.Clean(u.Path)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			// A member outside the tree would make the same commit verify
			// differently per machine: hermeticity is refused away, never
			// silently bent.
			return nil, fmt.Errorf("go.work member %q escapes the verification tree; members must lie within it", u.Path)
		}
		// The lexical check above refuses what the committed text says;
		// the resolved check refuses what the filesystem makes of it (an
		// in-tree symlink pointing out).
		if err := resolvedUnder(root, dir, clean); err != nil {
			return nil, fmt.Errorf("go.work member %q: %w", u.Path, err)
		}
		members = append(members, clean)
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
	work := filepath.Join(dir, "go.work")
	pin := "GOWORK=off"
	if _, err := os.Stat(work); err == nil {
		if abs, aerr := filepath.Abs(work); aerr == nil {
			work = abs
		}
		pin = "GOWORK=" + work
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
	env = setEnv(env, "GOPACKAGESDRIVER", "off")
	env = setEnv(env, "GOWORK", strings.TrimPrefix(pin, "GOWORK="))
	// The toolchain's telemetry is owned at this root as at the policy
	// normalizer's (telemetry.go).
	return telemetryOffEnv(env)
}
