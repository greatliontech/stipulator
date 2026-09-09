package golang

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/greatliontech/stipulator/internal/recordstore"
)

// The toolchain's telemetry, on by default, forks a detached upload
// sidecar on its daily check — setsid, outside the process group every
// Go child of this backend runs in — and no variable names the
// telemetry directory (the go command lists GOTELEMETRYDIR as an output
// it refuses to set; x/telemetry resolves it under the user config
// home). The config home the toolchain resolves it under is therefore
// the one seam, and on unix that home is XDG_CONFIG_HOME. Every Go
// child's environment passes through telemetryOffEnv at its root — the
// policy normalizer's before the load-time query, goworkEnv's for the
// symbol loads, the served views, and the toolchain queries — so the
// descendant tree is exactly what the runner owns
// (REQ-go-owned-processes).
//
// The owned home lives under the user cache root — or, when that root
// cannot host it (an unwritable cache root, under which only the
// witness store refuses, per subject), under /tmp, so a verb that
// loads symbols still runs — one per SOURCE home: the config home the
// child would have read, resolved from the child's own environment the
// way the toolchain resolves it, its XDG_CONFIG_HOME, else its HOME's
// .config; a child with neither has no config home and, by the
// toolchain's own rule, no telemetry, so nothing is owned or pinned.
// Each root is a directory of the calling user's own, private (0700),
// never a symlink, secured before use — under a world-writable parent
// a claimed root would let a co-tenant write the git config a module
// fetch's git executes. The fallback is /tmp literally, never $TMPDIR:
// the owned home's path is a recorded environment coordinate (it rides
// the invocation environment into the witness-cache key), so its
// stability is the host's, and a session-scoped TMPDIR would move it
// every session. The home is created once and never swept: under mode
// off the toolchain writes nothing there. A home swept from under a
// running check by something else is re-established before every
// spawn that reuses a derived environment (ensureTelemetryOwned). A self-hosted run nests: the outer owned home is the
// inner run's source, and a further owned home is derived from it;
// owned homes accumulate one per source and are swept by nothing, by
// design. The whole config home moves — anything else a child reads
// under it moves too — and what a Go child does read there is pinned
// back to the source: the go command's own env file, by GOENV naming
// the source's unless the environment names one (the normalizer later
// pins GOENV off for every spawn; the load-time query is where this
// pin bears), and git's global configuration with its default ignore
// and attributes files (a module fetch under -mod=mod spawns git,
// which reads them under <config home>/git/), by an owned git/config
// naming the source's as defaults and including the source's config
// last, so the source's own settings win. Elsewhere (darwin, windows)
// no variable selects the config home short of HOME/AppData, and the
// toolchain's detached telemetry stands as the one sanctioned escape.

// telemetryOffEnv is env with the toolchain's telemetry owned, on the
// platforms whose config home a variable selects; a home that cannot be
// prepared is a refusal naming the reason — a witness whose descendant
// tree the runner cannot own does not run.
func telemetryOffEnv(env []string) ([]string, error) {
	env, _, err := telemetryOffEnvSource(env)
	return env, err
}

// telemetryOffEnvSource is telemetryOffEnv returning the source home
// too — the fact a later re-establishment of the owned home needs.
func telemetryOffEnvSource(env []string) ([]string, string, error) {
	return telemetryOffEnvSourceFor(runtime.GOOS, env)
}

// telemetryOffEnvFor is telemetryOffEnv for one platform, so the
// non-unix arm is a unit fact.
func telemetryOffEnvFor(goos string, env []string) ([]string, error) {
	env, _, err := telemetryOffEnvSourceFor(goos, env)
	return env, err
}

func telemetryOffEnvSourceFor(goos string, env []string) ([]string, string, error) {
	if !configHomeSelectable(goos) {
		return env, "", nil
	}
	source, err := sourceConfigHome(env)
	if err != nil {
		return nil, "", fmt.Errorf("owning the toolchain's telemetry: %w", err)
	}
	if source == "" {
		return env, "", nil
	}
	home, err := telemetryOffHome(source)
	if err != nil {
		return nil, "", fmt.Errorf("owning the toolchain's telemetry: %w", err)
	}
	if _, ok := lookupEnv(env, "GOENV"); !ok {
		env = setEnv(env, "GOENV", filepath.Join(source, "go", "env"))
	}
	return setEnv(env, "XDG_CONFIG_HOME", home), source, nil
}

// configHomeSelectable reports the platforms whose config home a
// variable selects — the seam exists there and nowhere else.
func configHomeSelectable(goos string) bool {
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos":
		return true
	}
	return false
}

// telemetryTempRoot is the fallback root's parent: /tmp literally, a
// variable only so tests plant roots without touching the machine's.
var telemetryTempRoot = "/tmp"

// telemetryOffMode is the mode file's content: the word the toolchain's
// reader honours, trimmed (an optional " <date>" suffix is its own).
const telemetryOffMode = "off\n"

// telemetryOffHome is the owned config home for one source home:
// <user cache>/stipulator/telemetry-off/<digest of the source path>,
// or /tmp/stipulator-telemetry-off-<uid>/<digest> when the cache root
// cannot host it — each root secured first, the first root that
// prepares wins, and both failing is the refusal naming each — holding
// go/telemetry/mode = off and a git/config that names the source
// home's ignore and attributes files as git's defaults and includes the
// source home's config (git ignores an include whose file is absent).
// Each file is written only when its content is not already in place,
// and atomically — a reader never sees a partial mode file, which the
// toolchain would read as a mode other than off.
func telemetryOffHome(source string) (string, error) {
	sum := sha256.Sum256([]byte(source))
	key := hex.EncodeToString(sum[:8])
	var roots []string
	if root, err := recordstore.Root("telemetry-off"); err == nil {
		roots = append(roots, root)
	}
	roots = append(roots, filepath.Join(telemetryTempRoot, "stipulator-telemetry-off-"+strconv.Itoa(os.Getuid())))
	var errs []error
	for _, root := range roots {
		if err := secureRoot(root); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", root, err))
			continue
		}
		home := filepath.Join(root, key)
		if err := prepareTelemetryOffHome(home, source); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", root, err))
			continue
		}
		return home, nil
	}
	return "", errors.Join(errs...)
}

// secureRoot makes root a directory of the calling user's own and
// private: created 0700 when absent (its parent as needed), and
// refused unless what stands there is a directory — never a symlink —
// owned by the caller; a root of the caller's own with group or other
// bits is tightened to 0700, since ownership is the fact and the mode
// is the repair. The check holds only under a parent nobody else can
// rename the root out of — sticky, or the caller's or root's own — so
// that premise is checked too, never assumed of /tmp.
func secureRoot(root string) error {
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(parent); err != nil {
		return err
	} else if !info.IsDir() || !(info.Mode()&os.ModeSticky != 0 || ownedByCaller(info) || ownedByRoot(info)) {
		return fmt.Errorf("%s: its parent is not a directory that is sticky or this user's own", root)
	}
	if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	if !ownedByCaller(info) {
		return fmt.Errorf("%s is not owned by this user", root)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(root, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// prepareTelemetryOffHome writes the owned home's two files under
// home, each only when its content is not already in place.
func prepareTelemetryOffHome(home, source string) error {
	files := []struct{ path, content string }{
		{filepath.Join(home, "go", "telemetry", "mode"), telemetryOffMode},
		{filepath.Join(home, "git", "config"), gitConfigFor(source)},
	}
	for _, f := range files {
		if data, err := os.ReadFile(f.path); err == nil && string(data) == f.content {
			continue
		}
		if err := writeFileAtomic(f.path, []byte(f.content)); err != nil {
			return err
		}
	}
	return nil
}

// gitConfigFor is the owned git/config for one source home: the
// source's ignore and attributes files as git's defaults, then the
// source's config included, so the source's own settings win.
func gitConfigFor(source string) string {
	return "[core]\n\texcludesFile = " + filepath.Join(source, "git", "ignore") +
		"\n\tattributesFile = " + filepath.Join(source, "git", "attributes") +
		"\n[include]\n\tpath = " + filepath.Join(source, "git", "config") + "\n"
}

// sourceConfigHome is the config home the child would have read,
// resolved as the toolchain resolves it over the child's own
// environment: XDG_CONFIG_HOME when set, else HOME's .config, else none
// — a child with neither has no config home, and the toolchain's own
// rule leaves its telemetry off. One directory is one source whatever
// its spelling (cleaned, symlinks resolved where they resolve), and a
// source that is not absolute is refused — the toolchain itself refuses
// it, and a relative include or GOENV would resolve against the wrong
// directory.
func sourceConfigHome(env []string) (string, error) {
	var source string
	switch {
	case hasEnv(env, "XDG_CONFIG_HOME"):
		source, _ = lookupEnv(env, "XDG_CONFIG_HOME")
	case hasEnv(env, "HOME"):
		home, _ := lookupEnv(env, "HOME")
		source = filepath.Join(home, ".config")
	default:
		return "", nil
	}
	if !filepath.IsAbs(source) {
		return "", fmt.Errorf("config home %q is not absolute", source)
	}
	return resolveOrSelf(filepath.Clean(source)), nil
}

// hasEnv reports a key set to a non-empty value.
func hasEnv(env []string, key string) bool {
	v, ok := lookupEnv(env, key)
	return ok && v != ""
}

// telemetryOwned reports whether env's toolchain telemetry is owned: on
// a platform whose config home a variable selects, XDG_CONFIG_HOME names
// a home whose mode file is the owned one, or the environment has no
// config home at all. The load-time query refuses an environment that
// is not, so the ordering of the two roots' pins cannot be undone by a
// refactor that leaves every later child owned; a home deleted between
// derivation and query reads as not owned there — the spawn-time
// re-establishment, not the query, is the repair.
func telemetryOwned(env []string) bool {
	return telemetryOwnedFor(runtime.GOOS, env)
}

func telemetryOwnedFor(goos string, env []string) bool {
	if !configHomeSelectable(goos) {
		return true
	}
	if !hasEnv(env, "XDG_CONFIG_HOME") {
		return !hasEnv(env, "HOME")
	}
	home, _ := lookupEnv(env, "XDG_CONFIG_HOME")
	data, err := os.ReadFile(filepath.Join(home, "go", "telemetry", "mode"))
	return err == nil && string(data) == telemetryOffMode
}

// ensureTelemetryOwned re-establishes env's owned home before a spawn
// that reuses a derived environment: a home swept from under a running
// check is prepared again from the recorded source, in place — the
// environment's own home, its root secured first, never a freshly
// chosen one, since the home is a recorded coordinate a mid-run move
// would split from the executed environment — and refuses when it
// cannot.
func ensureTelemetryOwned(env []string, source string) error {
	if telemetryOwned(env) {
		return nil
	}
	if !hasEnv(env, "XDG_CONFIG_HOME") || source == "" {
		return fmt.Errorf("owning the toolchain's telemetry: the environment's config home is not the owned one")
	}
	home, _ := lookupEnv(env, "XDG_CONFIG_HOME")
	if err := secureRoot(filepath.Dir(home)); err != nil {
		return fmt.Errorf("owning the toolchain's telemetry: re-establishing %s: %w", home, err)
	}
	if err := prepareTelemetryOffHome(home, source); err != nil {
		return fmt.Errorf("owning the toolchain's telemetry: re-establishing %s: %w", home, err)
	}
	if !telemetryOwned(env) {
		return fmt.Errorf("owning the toolchain's telemetry: %s is not owned after re-establishment", home)
	}
	return nil
}

// writeFileAtomic writes content to path through a temporary sibling
// and a rename, creating the directory; a concurrent reader sees the
// prior file or the whole new one, never a partial write. A process
// dying between the two leaves the sibling behind — inert to every
// reader of the directory, and removed by nothing.
func writeFileAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}
