package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// NormalizedInvocation is one Go policy invocation with every pin-at-load
// field resolved to its concrete effective value: what will actually run,
// visible in full. It is in-memory only — environment-derived pins never
// enter the committed record, which stays a pure function of the workspace
// declaration (the derived-record byte-determinism contract).
type NormalizedInvocation struct {
	// Name is the canonical invocation identity from the policy envelope.
	Name string
	// ModuleRoot is the tree-relative slash module root ("" = tree root).
	ModuleRoot string
	// Dir is the absolute host directory of the module root.
	Dir string
	// Packages is the invocation's package pattern scope.
	Packages []string
	Race     bool
	// Timeout is the envelope's explicit, reviewed timeout.
	Timeout time.Duration
	// Toolchain is the effective toolchain identity (`go env GOVERSION`).
	Toolchain  string
	GOOS       string
	GOARCH     string
	CgoEnabled bool
	Tags       []string
	// GOFLAGS is the effective, validated GOFLAGS value.
	GOFLAGS string
	// GOEXPERIMENT is the effective experiment set pinned at load
	// (`go env GOEXPERIMENT`); the committed record cannot set it, but the
	// run's set is part of what ran and rides the evidentiary record.
	GOEXPERIMENT string
	// WorkspaceOn reports whether the invocation runs under the tree's
	// go.work.
	WorkspaceOn bool
	ModuleMode  stipulatorv1.GoModuleMode
	PGO         string
	Count       int32
	CacheBypass bool
	Args        []string
	// Env is the complete normalized child-process environment every
	// subprocess of this invocation runs under: inherited minus denials,
	// plus overrides, with every backend-pinned key set from its one
	// typed source.
	Env []string
}

// NormalizeInvocation resolves one policy invocation against the tree at
// dir and the current process environment: absent pin-at-load fields pin
// the effective values the environment selects now, explicit fields
// override it, and unsupported ambient controls (the overlay class, an
// external package driver) are refused. The one toolchain query it makes
// runs inside the same owned, cancellable process boundary as every other
// child of policy work (REQ-go-owned-processes).
func NormalizeInvocation(ctx context.Context, dir string, inv *stipulatorv1.PolicyInvocation) (*NormalizedInvocation, error) {
	cfg := inv.GetGo()
	if cfg == nil {
		return nil, fmt.Errorf("invocation %q carries no Go payload", inv.GetName())
	}
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invocation %q: %w", inv.GetName(), err)
	}
	env, err := normalizeEnv(os.Environ())
	if err != nil {
		return nil, fmt.Errorf("invocation %q: inherited environment: %w", inv.GetName(), err)
	}
	// An ambient external package driver never shapes verification: refuse
	// a real driver, then pin the variable off so nothing downstream can
	// re-inherit one (aligned with gofresh's package-loading refusal).
	if driver, ok := lookupEnv(env, "GOPACKAGESDRIVER"); ok && driver != "" && driver != "off" {
		return nil, fmt.Errorf("invocation %q: GOPACKAGESDRIVER=%q is unsupported; an ambient package driver must never shape verification", inv.GetName(), driver)
	}
	for _, name := range cfg.GetEnvDeny() {
		env = dropEnv(env, name)
	}
	for _, e := range cfg.GetEnvironment() {
		env = setEnv(env, e[:strings.IndexByte(e, '=')], e[strings.IndexByte(e, '=')+1:])
	}
	env = setEnv(env, "GOPACKAGESDRIVER", "off")

	n := &NormalizedInvocation{
		Name:       inv.GetName(),
		ModuleRoot: cfg.GetModuleRoot(),
		Packages:   append([]string(nil), cfg.GetPackages()...),
		Race:       cfg.GetRace(),
		Timeout:    inv.GetTimeout().AsDuration(),
		Tags:       append([]string(nil), cfg.GetTags()...),
		ModuleMode: cfg.GetModuleMode(),
		Count:      cfg.GetCount(),
		Args:       append([]string(nil), cfg.GetArgs()...),
	}
	if cfg.HasPgo() {
		n.PGO = cfg.GetPgo()
	}
	n.CacheBypass = cfg.GetCacheMode() == stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS

	abs, err := filepath.Abs(filepath.Join(dir, filepath.FromSlash(cfg.GetModuleRoot())))
	if err != nil {
		return nil, fmt.Errorf("invocation %q: resolving module root: %w", inv.GetName(), err)
	}
	n.Dir = abs

	// Workspace mode: absent derives from the workspace declaration.
	work := filepath.Join(dir, "go.work")
	_, workErr := os.Stat(work)
	hasWork := workErr == nil
	switch cfg.GetWorkspaceMode() {
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_WORKSPACE:
		if !hasWork {
			return nil, fmt.Errorf("invocation %q: workspace_mode WORKSPACE but the tree declares no go.work", inv.GetName())
		}
		n.WorkspaceOn = true
	case stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF:
		n.WorkspaceOn = false
	default:
		n.WorkspaceOn = hasWork
	}
	if n.WorkspaceOn {
		if workAbs, err := filepath.Abs(work); err == nil {
			work = workAbs
		}
		env = setEnv(env, "GOWORK", work)
	} else {
		env = setEnv(env, "GOWORK", "off")
	}

	// Explicit platform and build pins land in the child environment
	// before the effective query, so the query answers for the pinned
	// configuration.
	if cfg.HasToolchain() {
		env = setEnv(env, "GOTOOLCHAIN", cfg.GetToolchain())
	}
	if cfg.HasGoos() {
		env = setEnv(env, "GOOS", cfg.GetGoos())
	}
	if cfg.HasGoarch() {
		env = setEnv(env, "GOARCH", cfg.GetGoarch())
	}
	if cfg.HasCgoEnabled() {
		v := "0"
		if cfg.GetCgoEnabled() {
			v = "1"
		}
		env = setEnv(env, "CGO_ENABLED", v)
	}
	if cfg.HasGoflags() {
		env = setEnv(env, "GOFLAGS", cfg.GetGoflags())
	}

	version, goos, goarch, cgo, goflags, goexperiment, err := effectiveGoEnv(ctx, n.Dir, env)
	if err != nil {
		return nil, fmt.Errorf("invocation %q: %w", inv.GetName(), err)
	}
	// The effective GOFLAGS covers the ambient variable and the go env
	// config file alike; validate whichever source won. The explicit field
	// was already statically validated, so a failure here always names an
	// ambient control.
	if err := validateGoflags(goflags); err != nil {
		return nil, fmt.Errorf("invocation %q: ambient control: %w", inv.GetName(), err)
	}
	n.Toolchain, n.GOOS, n.GOARCH, n.GOFLAGS = version, goos, goarch, goflags
	n.GOEXPERIMENT = goexperiment
	n.CgoEnabled = cgo == "1"
	// Pin every effective value into the child environment: later spawns
	// run under the values resolved at load even if the host environment
	// or go env config moves in between.
	env = setEnv(env, "GOOS", goos)
	env = setEnv(env, "GOARCH", goarch)
	env = setEnv(env, "CGO_ENABLED", cgo)
	env = setEnv(env, "GOFLAGS", goflags)
	// The persistent go env config file is a second ambient source the
	// frozen environment cannot freeze: a go env -w between load and spawn
	// would move the toolchain or experiments under a pinned record. GOENV
	// off makes the pinned environment the only source; the resolved
	// toolchain and experiment set are pinned explicitly. A development
	// toolchain version is not a valid GOTOOLCHAIN value, so it pins local.
	env = setEnv(env, "GOENV", "off")
	if inv.GetGo().GetToolchain() == "" {
		toolchainPin := version
		if !strings.HasPrefix(version, "go") {
			toolchainPin = "local"
		}
		env = setEnv(env, "GOTOOLCHAIN", toolchainPin)
	}
	env = setEnv(env, "GOEXPERIMENT", goexperiment)
	n.Env = env
	return n, nil
}

// effectiveGoEnv queries the exec'd toolchain for the pin-at-load values in
// one owned, cancellable subprocess.
func effectiveGoEnv(ctx context.Context, dir string, env []string) (version, goos, goarch, cgo, goflags, goexperiment string, err error) {
	cmd := commandContext(ctx, "go", "env", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOEXPERIMENT")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("resolving effective go env: %w", err)
	}
	// Strip exactly the final newline: an empty value (an unset GOFLAGS)
	// is a legitimate empty line that TrimRight would swallow.
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 6 {
		return "", "", "", "", "", "", fmt.Errorf("unexpected go env output %q", out)
	}
	return lines[0], lines[1], lines[2], lines[3], lines[4], lines[5], nil
}

// normalizeEnv returns a deterministic owned copy of a complete process
// environment, refusing malformed entries and duplicate keys instead of
// resolving them by platform-dependent first- or last-entry behavior —
// the same contract gofresh's environment normalization enforces, so an
// environment built here survives the freshness engine unchanged.
func normalizeEnv(env []string) ([]string, error) {
	out := make([]string, len(env))
	seen := make(map[string]bool, len(env))
	for i, entry := range env {
		if strings.ContainsRune(entry, 0) {
			return nil, fmt.Errorf("environment entry %d contains NUL", i)
		}
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("environment entry %d is malformed: expected non-empty key=value", i)
		}
		key := entry[:eq]
		if seen[key] {
			return nil, fmt.Errorf("environment contains duplicate key %q", key)
		}
		seen[key] = true
		out[i] = entry
	}
	sort.Strings(out)
	return out, nil
}

// setEnv replaces or inserts key in a normalized environment, preserving
// sortedness and the single-entry-per-key invariant.
func setEnv(env []string, key, value string) []string {
	out := dropEnv(env, key)
	entry := key + "=" + value
	i := sort.SearchStrings(out, entry)
	out = append(out, "")
	copy(out[i+1:], out[i:])
	out[i] = entry
	return out
}

// dropEnv removes key from a normalized environment.
func dropEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if eq := strings.IndexByte(entry, '='); eq > 0 && entry[:eq] == key {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// lookupEnv returns key's value from a normalized environment.
func lookupEnv(env []string, key string) (string, bool) {
	for _, entry := range env {
		if eq := strings.IndexByte(entry, '='); eq > 0 && entry[:eq] == key {
			return entry[eq+1:], true
		}
	}
	return "", false
}
