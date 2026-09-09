package golang

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ownedHome asserts env carries an owned telemetry-off home under cache
// and returns it.
func ownedHome(t *testing.T, env []string, cache string) string {
	t.Helper()
	home, ok := lookupEnv(env, "XDG_CONFIG_HOME")
	if !ok || filepath.Dir(home) != filepath.Join(cache, "stipulator", "telemetry-off") {
		t.Fatalf("XDG_CONFIG_HOME = %q, %v; want an owned home under the cache root", home, ok)
	}
	return home
}

// On unix the environment's config home becomes the owned telemetry-off
// home for the child's source home — one per source, holding the off
// mode and a git config including the source's — with GOENV pinned to
// the source home's env file: the default config home's when the
// environment names neither, its own config home's when it names one,
// and its own GOENV untouched (REQ-go-owned-processes).
func TestTelemetryOffEnvOwnsTheToolchainTelemetry(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	userHome := t.TempDir()
	defaultCfg := filepath.Join(userHome, ".config")
	env, err := telemetryOffEnv([]string{"HOME=" + userHome, "PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	home := ownedHome(t, env, cache)
	if data, err := os.ReadFile(filepath.Join(home, "go", "telemetry", "mode")); err != nil || string(data) != "off\n" {
		t.Fatalf("mode file = %q, %v; want off", data, err)
	}
	if gitcfg, err := os.ReadFile(filepath.Join(home, "git", "config")); err != nil || string(gitcfg) != "[core]\n\texcludesFile = "+filepath.Join(defaultCfg, "git", "ignore")+"\n\tattributesFile = "+filepath.Join(defaultCfg, "git", "attributes")+"\n[include]\n\tpath = "+filepath.Join(defaultCfg, "git", "config")+"\n" {
		t.Fatalf("git config = %q, %v; want the source home's defaults and an include of its config", gitcfg, err)
	}
	if goenv, ok := lookupEnv(env, "GOENV"); !ok || goenv != filepath.Join(defaultCfg, "go", "env") {
		t.Fatalf("GOENV = %q, %v; want the default config home's env file", goenv, ok)
	}
	// An environment naming its own config home is the source: its env
	// file named, its git config included, its owned home its own.
	env, err = telemetryOffEnv([]string{"XDG_CONFIG_HOME=/srv/cfg"})
	if err != nil {
		t.Fatal(err)
	}
	if goenv, _ := lookupEnv(env, "GOENV"); goenv != "/srv/cfg/go/env" {
		t.Fatalf("GOENV under an environment's own config home = %q", goenv)
	}
	own := ownedHome(t, env, cache)
	if own == home {
		t.Fatal("two source homes share one owned home")
	}
	if gitcfg, err := os.ReadFile(filepath.Join(own, "git", "config")); err != nil || !strings.Contains(string(gitcfg), "path = /srv/cfg/git/config") {
		t.Fatalf("git config under an environment's own source = %q, %v", gitcfg, err)
	}
	// An environment's own HOME is the source when it names no
	// XDG_CONFIG_HOME — the toolchain's own resolution.
	env, err = telemetryOffEnv([]string{"HOME=/hermetic"})
	if err != nil {
		t.Fatal(err)
	}
	if goenv, _ := lookupEnv(env, "GOENV"); goenv != "/hermetic/.config/go/env" {
		t.Fatalf("GOENV under an environment's own HOME = %q", goenv)
	}
	hermetic := ownedHome(t, env, cache)
	if hermetic == home || hermetic == own {
		t.Fatal("an environment's own HOME shares another source's owned home")
	}
	if gitcfg, err := os.ReadFile(filepath.Join(hermetic, "git", "config")); err != nil || !strings.Contains(string(gitcfg), "path = /hermetic/.config/git/config") {
		t.Fatalf("git config under an environment's own HOME = %q, %v", gitcfg, err)
	}
	// One directory is one source whatever its spelling: a trailing
	// slash and a symlink resolve to the same owned home.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var homes []string
	for _, spelling := range []string{real, real + "/", link} {
		env, err := telemetryOffEnv([]string{"XDG_CONFIG_HOME=" + spelling})
		if err != nil {
			t.Fatal(err)
		}
		homes = append(homes, ownedHome(t, env, cache))
	}
	if homes[0] != homes[1] || homes[0] != homes[2] {
		t.Fatalf("one directory, three owned homes: %v", homes)
	}
	// A source that is not absolute is a refusal.
	if _, err := telemetryOffEnv([]string{"XDG_CONFIG_HOME=cfg"}); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("a relative config home: %v; want a refusal", err)
	}
	// An environment's own GOENV is kept.
	env, err = telemetryOffEnv([]string{"GOENV=/srv/own.env", "HOME=/h"})
	if err != nil {
		t.Fatal(err)
	}
	if goenv, _ := lookupEnv(env, "GOENV"); goenv != "/srv/own.env" {
		t.Fatalf("an environment's own GOENV moved: %q", goenv)
	}
	// A child with neither XDG_CONFIG_HOME nor HOME has no config home
	// and nothing to own: the environment passes unchanged.
	env, err = telemetryOffEnv([]string{"PATH=/usr/bin"})
	if err != nil || len(env) != 1 || env[0] != "PATH=/usr/bin" {
		t.Fatalf("a child with no config home: %v, %v; want the environment unchanged", env, err)
	}
}

// The owned home is repaired when its files hold anything but the owned
// content — an empty or local mode a toolchain would honour — and left
// untouched when they already do (REQ-go-owned-processes).
func TestTelemetryOffHomeIsRepairedNotRewritten(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	home, err := telemetryOffHome("/srv/cfg")
	if err != nil {
		t.Fatal(err)
	}
	mode := filepath.Join(home, "go", "telemetry", "mode")
	for _, planted := range []string{"", "local\n", "on 2026-09-09\n"} {
		if err := os.WriteFile(mode, []byte(planted), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := telemetryOffHome("/srv/cfg"); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(mode); string(data) != "off\n" {
			t.Fatalf("planted %q: mode = %q after the call, want off", planted, data)
		}
	}
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(mode, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := telemetryOffHome("/srv/cfg"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(mode); err != nil || !info.ModTime().Equal(old) {
		t.Fatalf("an owned mode file was rewritten: %v %v", info.ModTime(), err)
	}
	// No temporary sibling survives a write.
	entries, _ := os.ReadDir(filepath.Dir(mode))
	if len(entries) != 1 {
		t.Fatalf("the mode directory holds %d entries, want the mode alone", len(entries))
	}
}

// A toolchain run under the owned home writes nothing there — the
// telemetry's local directory and counters, live under any other
// mode, never appear (REQ-go-owned-processes).
func TestToolchainWritesNothingUnderTheOwnedHome(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	env, err := telemetryOffEnv(os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	home := ownedHome(t, env, cache)
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go env under the owned home: %v: %s", err, out)
	}
	var files []string
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(home, p)
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	if want := []string{filepath.Join("git", "config"), filepath.Join("go", "telemetry", "mode")}; strings.Join(files, ",") != strings.Join(want, ",") {
		t.Fatalf("owned home holds %v after a toolchain run, want %v", files, want)
	}
}

// Off unix no variable selects the config home: the environment passes
// through unchanged, the toolchain's detached telemetry the sanctioned
// escape (REQ-go-owned-processes).
func TestTelemetryOffEnvLeavesOtherPlatformsAlone(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	in := []string{"HOME=/h", "PATH=/usr/bin"}
	for _, goos := range []string{"darwin", "windows", "plan9", "js"} {
		out, err := telemetryOffEnvFor(goos, in)
		if err != nil || len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
			t.Fatalf("%s: %v, %v; want the environment unchanged", goos, out, err)
		}
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatalf("a non-unix derivation prepared %d entries under the cache", len(entries))
	}
}

// A cache root that cannot host the owned home falls back to the
// system temp root — the witness store alone refuses under an
// unwritable cache, per subject — and both failing is a refusal naming
// each root, never a child whose descendants the runner does not own
// (REQ-go-owned-processes).
func TestTelemetryOffEnvRefusesWithoutAnOwnedHome(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	if telemetryTempRoot != "/tmp" {
		t.Fatalf("the fallback root's parent is %q; want /tmp literally, never $TMPDIR", telemetryTempRoot)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", blocked)
	swapTempRoot(t, tmp)
	env, err := telemetryOffEnv([]string{"HOME=/h", "PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("a cache root that is a file, a temp root that is not: %v; want the temp root", err)
	}
	root := filepath.Join(tmp, "stipulator-telemetry-off-"+strconv.Itoa(os.Getuid()))
	if home, _ := lookupEnv(env, "XDG_CONFIG_HOME"); filepath.Dir(home) != root {
		t.Fatalf("owned home under a blocked cache = %q; want the temp root", home)
	}
	if info, err := os.Lstat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("fallback root mode = %v, %v; want 0700", info, err)
	}
	swapTempRoot(t, blocked)
	_, err = telemetryOffEnv([]string{"HOME=/h", "PATH=/usr/bin"})
	if err == nil || !strings.Contains(err.Error(), "owning the toolchain's telemetry") || !strings.Contains(err.Error(), "stipulator/telemetry-off") || !strings.Contains(err.Error(), "stipulator-telemetry-off-") {
		t.Fatalf("both roots blocked: %v; want a refusal naming each", err)
	}
	if _, err := goworkEnv(t.TempDir()); err == nil {
		t.Fatal("goworkEnv served an environment whose telemetry it could not own")
	}
}

func swapTempRoot(t *testing.T, root string) {
	t.Helper()
	prior := telemetryTempRoot
	telemetryTempRoot = root
	t.Cleanup(func() { telemetryTempRoot = prior })
}

// A root under a world-writable parent is the user's own or refused: a
// symlink standing where the root belongs is a claim, refused by name;
// a directory of the user's own with loose bits is tightened to 0700
// and used (REQ-go-owned-processes).
func TestSecureRootRefusesAClaimedFallback(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	if os.Getuid() == 0 {
		t.Skip("root owns everything")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", blocked)
	tmp := t.TempDir()
	swapTempRoot(t, tmp)
	root := filepath.Join(tmp, "stipulator-telemetry-off-"+strconv.Itoa(os.Getuid()))
	if err := os.Symlink(t.TempDir(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := telemetryOffEnv([]string{"HOME=/h"}); err == nil || !strings.Contains(err.Error(), root) || !strings.Contains(err.Error(), root+" is not a directory") {
		t.Fatalf("a symlinked fallback root: %v; want a refusal naming it", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatal(err)
	}
	env, err := telemetryOffEnv([]string{"HOME=/h"})
	if err != nil {
		t.Fatalf("a loose root of this user's own: %v; want it tightened and used", err)
	}
	if home, _ := lookupEnv(env, "XDG_CONFIG_HOME"); filepath.Dir(home) != root {
		t.Fatalf("owned home = %q; want it under the tightened root", home)
	}
	if info, err := os.Lstat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode after use = %v, %v; want 0700", info, err)
	}
}

// A home swept from under a running check is re-established from the
// recorded source before the next spawn that reuses the derived
// environment; a home that cannot be is a refusal (REQ-go-owned-processes).
func TestOwnedHomeIsReestablishedBeforeSpawn(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	env, source, err := telemetryOffEnvSource([]string{"HOME=/h", "PATH=/usr/bin"})
	if err != nil || source != "/h/.config" {
		t.Fatalf("derivation: %v, %q", err, source)
	}
	home := ownedHome(t, env, cache)
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if telemetryOwned(env) {
		t.Fatal("a swept home passed as owned")
	}
	if err := ensureTelemetryOwned(env, source); err != nil {
		t.Fatalf("re-establishment: %v", err)
	}
	if !telemetryOwned(env) {
		t.Fatal("the home was not re-established")
	}
	if data, _ := os.ReadFile(filepath.Join(home, "git", "config")); !strings.Contains(string(data), "path = /h/.config/git/config") {
		t.Fatalf("the re-established git config = %q", data)
	}
	// Without the recorded source nothing can be re-established.
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := ensureTelemetryOwned(env, ""); err == nil {
		t.Fatal("a swept home with no recorded source was not refused")
	}
	// An environment with no config home is owned as it stands.
	if err := ensureTelemetryOwned([]string{"PATH=/usr/bin"}, ""); err != nil {
		t.Fatalf("a child with no config home: %v", err)
	}
	// Re-establishment goes through the secured root: a swept fallback
	// ROOT replaced by a claim is refused by name, and once the claim is
	// gone the root comes back private.
	if os.Getuid() == 0 {
		return
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", blocked)
	tmp := t.TempDir()
	swapTempRoot(t, tmp)
	env, source, err = telemetryOffEnvSource([]string{"HOME=/h", "PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "stipulator-telemetry-off-"+strconv.Itoa(os.Getuid()))
	home, _ = lookupEnv(env, "XDG_CONFIG_HOME")
	if filepath.Dir(home) != root {
		t.Fatalf("fallback home = %q", home)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), root); err != nil {
		t.Fatal(err)
	}
	if err := ensureTelemetryOwned(env, source); err == nil || !strings.Contains(err.Error(), root+" is not a directory") {
		t.Fatalf("a claimed root at re-establishment: %v; want a refusal naming it", err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := ensureTelemetryOwned(env, source); err != nil {
		t.Fatalf("re-establishing the root: %v", err)
	}
	if info, err := os.Lstat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("re-established root mode = %v, %v; want 0700", info, err)
	}
	// The cache root hostable again does not move the home: the
	// environment's own — the recorded coordinate — is re-established
	// in place, and a fresh derivation would have chosen the cache.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := ensureTelemetryOwned(env, source); err != nil {
		t.Fatalf("re-establishing the environment's own home with the cache hostable: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "go", "telemetry", "mode")); err != nil || string(data) != "off\n" {
		t.Fatalf("the environment's own home was not re-established: %q, %v", data, err)
	}
	if fresh, err := telemetryOffHome(source); err != nil || fresh == home {
		t.Fatalf("fixture: a fresh derivation = %q, %v; want the cache's home, not the environment's", fresh, err)
	}
}

// A home swept after normalization is re-established by the witness
// spawn itself: the fixture's package runs healthy and the mode file is
// back before its process started (REQ-go-owned-processes).
func TestWitnessSpawnReestablishesTheOwnedHome(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	if testing.Short() {
		t.Skip("executes the fixture's tests")
	}
	neutralAmbient(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("swept")
	inv.SetTimeout(durationpb.New(2 * time.Minute))
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	inv.SetGo(cfg)
	ctx := context.Background()
	n, err := NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	home := ownedHome(t, n.Env, cache)
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	health, _, _, _, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "go", "telemetry", "mode")); err != nil || string(data) != "off\n" {
		t.Fatalf("the spawn did not re-establish the home: %q, %v", data, err)
	}
	if got := health.GetDisposition(); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY {
		t.Fatalf("the swept-and-re-established run's disposition = %v", got)
	}
	// A home that cannot be re-established at spawn — its root replaced
	// by a claim — degrades the package with the reason, never spawns.
	if os.Getuid() == 0 {
		return
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", blocked)
	// Only the owned home's cache root is blocked: the toolchain's build
	// cache stays writable, so the run reaches the spawn.
	t.Setenv("GOCACHE", t.TempDir())
	tmp := t.TempDir()
	swapTempRoot(t, tmp)
	inv.SetName("claimed")
	n, err = NormalizeInvocation(ctx, executeFixture(t), inv)
	if err != nil {
		t.Fatal(err)
	}
	obs, err = DiscoverInvocation(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "stipulator-telemetry-off-"+strconv.Itoa(os.Getuid()))
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), root); err != nil {
		t.Fatal(err)
	}
	health, _, diags, _, err := ExecuteInvocation(ctx, n, obs)
	if err != nil {
		t.Fatal(err)
	}
	if got := packageDisposition(t, health, "example.com/exec/ok"); got != stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED {
		t.Fatalf("a package whose home cannot be re-established: disposition = %v, want degraded", got)
	}
	named := false
	for _, d := range diags {
		if strings.Contains(d.GetOutput(), "owning the toolchain's telemetry") {
			named = true
		}
	}
	if !named {
		t.Fatalf("the degradation does not name the telemetry: %v", diags)
	}
}

// A root is secured only under a parent nobody else can rename it out
// of: a sticky parent (as /tmp is) and the user's own parent both
// qualify; the fact is checked, never assumed of the fallback
// (REQ-go-owned-processes).
func TestSecureRootChecksItsParent(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	if os.Getuid() == 0 {
		t.Skip("root owns everything")
	}
	sticky := t.TempDir()
	if err := os.Chmod(sticky, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if err := secureRoot(filepath.Join(sticky, "root")); err != nil {
		t.Fatalf("a sticky parent: %v", err)
	}
	if err := secureRoot(filepath.Join(t.TempDir(), "root")); err != nil {
		t.Fatalf("a parent of the user's own: %v", err)
	}
	// A parent that is a symlink is not the directory it names: refused
	// before any mkdir could follow it.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := secureRoot(filepath.Join(link, "root")); err == nil || !strings.Contains(err.Error(), "its parent is not a directory") {
		t.Fatalf("a symlinked parent: %v; want a refusal naming the parent", err)
	}
	if info, err := os.Lstat(sticky); err != nil || info.Mode()&os.ModeSticky == 0 || !ownedByCaller(info) {
		t.Fatalf("fixture: %v, %v", info, err)
	}
	// A parent that is neither cannot be constructed under one uid; the
	// predicate's negative arm is a unit fact over a plain file's info.
	plain := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := secureRoot(filepath.Join(plain, "root")); err == nil {
		t.Fatal("a parent that is a file was accepted")
	}
}

// The load-time query runs only under an owned environment: the pin's
// place before the query is a fact the query itself keeps, so no
// refactor re-opens the escape for that child with every later child
// still owned (REQ-go-owned-processes).
func TestLoadTimeQueryRefusesAnUnownedEnvironment(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if _, _, _, _, _, _, _, _, _, err := effectiveGoEnv(context.Background(), t.TempDir(), []string{"HOME=/h", "PATH=/usr/bin"}); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("an unowned query environment: %v; want a refusal", err)
	}
	if !telemetryOwned([]string{"PATH=/usr/bin"}) {
		t.Fatal("a child with no config home has nothing to own and must pass")
	}
	// A stale mode is not owned either — the home is repaired by the
	// derivation, never trusted by the query.
	env, err := telemetryOffEnv([]string{"HOME=/h", "PATH=/usr/bin"})
	if err != nil {
		t.Fatal(err)
	}
	home := ownedHome(t, env, cache)
	if err := os.WriteFile(filepath.Join(home, "go", "telemetry", "mode"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if telemetryOwned(env) {
		t.Fatal("a home whose mode is local passed as owned")
	}
	if !telemetryOwnedFor("darwin", []string{"PATH=/usr/bin"}) {
		t.Fatal("off unix every environment is owned — there is no seam")
	}
}

// Every Go child of an invocation runs under the owned home: the
// normalized environment, the witness environment derived from it, and
// the symbol-load environment all carry it (REQ-go-owned-processes).
func TestEveryGoChildRunsUnderTheOwnedHome(t *testing.T) {
	stipulate.Covers(t, "REQ-go-owned-processes")
	if runtime.GOOS != "linux" {
		t.Skip("the config-home seam is unix's")
	}
	neutralAmbient(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir := executeFixture(t)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName("owned")
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./ok"})
	inv.SetGo(cfg)
	n, err := NormalizeInvocation(context.Background(), dir, inv)
	if err != nil {
		t.Fatal(err)
	}
	home := ownedHome(t, n.Env, cache)
	if n.TelemetrySource == "" || !strings.HasSuffix(n.TelemetrySource, ".config") {
		t.Fatalf("the recorded source = %q; want the ambient HOME's .config", n.TelemetrySource)
	}
	if got := ownedHome(t, n.WitnessEnv, cache); got != home {
		t.Fatalf("witness env home %q, invocation env home %q", got, home)
	}
	if v, _ := lookupEnv(n.Env, "GOENV"); v != "off" {
		t.Fatalf("the spawn environment's GOENV = %q, want the normalizer's off pin kept", v)
	}
	loads, err := goworkEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ownedHome(t, loads, cache); got != home {
		t.Fatalf("symbol-load env home %q, invocation env home %q", got, home)
	}
}
