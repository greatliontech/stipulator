// Package testenv provides hermetic Go build-cache preservation and temporary
// XDG cache/state directory isolation for stipulator test suites.
package testenv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/greatliontech/stipulator/internal/gotool"
)

var (
	goCacheOnce sync.Once
	goCachePath string
)

// GoBuildCache resolves and memoizes the effective Go build cache directory
// once before suite or per-test XDG_CACHE_HOME overrides take effect, honoring
// GOENV configuration via gotool.GoBuildCache so child go invocations under
// GOENV=off keep a warm standard-library and package build cache.
func GoBuildCache() string {
	goCacheOnce.Do(func() {
		if v := os.Getenv("GOCACHE"); filepath.IsAbs(v) {
			goCachePath = v
			return
		}
		if dir, err := gotool.GoBuildCache(context.Background(), os.Environ()); err == nil && filepath.IsAbs(dir) {
			goCachePath = dir
			return
		}
		if dir, err := os.UserCacheDir(); err == nil && filepath.IsAbs(dir) {
			goCachePath = filepath.Join(dir, "go-build")
		}
	})
	return goCachePath
}

// IsolateMain runs m under a suite-scoped temporary XDG_CACHE_HOME and
// XDG_STATE_HOME while pinning GOCACHE to the host's warm Go build cache,
// removing the temporary cache tree before returning m.Run()'s exit code.
func IsolateMain(m *testing.M) int {
	return RunMain(m, nil)
}

// RunMain resolves GOCACHE, isolates XDG_CACHE_HOME and XDG_STATE_HOME under a
// temporary root removed on return (unless an ancestor TestMain already
// isolated the environment for a self-exec child helper), runs beforeRun (when
// non-nil) before the test harness starts testlog capture, and returns m.Run()'s
// exit code.
func RunMain(m *testing.M, beforeRun func()) int {
	isolated := os.Getenv("STIPULATOR_TESTENV_ISOLATED") != ""
	if !isolated {
		if goroot := runtime.GOROOT(); goroot != "" {
			goBin := filepath.Join(goroot, "bin")
			if info, err := os.Stat(goBin); err == nil && info.IsDir() {
				cur := os.Getenv("PATH")
				slots := filepath.SplitList(cur)
				if len(slots) == 0 || slots[0] != goBin {
					if cur != "" {
						_ = os.Setenv("PATH", goBin+string(os.PathListSeparator)+cur)
					} else {
						_ = os.Setenv("PATH", goBin)
					}
				}
			}
		}
	}
	if gocache := GoBuildCache(); gocache != "" && os.Getenv("GOCACHE") == "" {
		_ = os.Setenv("GOCACHE", gocache)
	}
	if !isolated {
		if root, err := os.MkdirTemp("", "stipulator-testcache-*"); err == nil {
			defer os.RemoveAll(root)
			cacheDir := filepath.Join(root, "cache")
			stateDir := filepath.Join(root, "state")
			_ = os.MkdirAll(cacheDir, 0o700)
			_ = os.MkdirAll(stateDir, 0o700)
			_ = os.Setenv("XDG_CACHE_HOME", cacheDir)
			_ = os.Setenv("XDG_STATE_HOME", stateDir)
			_ = os.Setenv("STIPULATOR_TESTENV_ISOLATED", "1")
		}
	}
	if beforeRun != nil {
		beforeRun()
	}
	return m.Run()
}

// TB is the subset of testing.TB consumed by NeutralAmbient.
type TB interface {
	Helper()
	Setenv(key, value string)
	TempDir() string
}

// NeutralAmbient pins the ambient Go toolchain and cache controls policy
// normalization and execution read to a hermetic state, so host configuration
// (GOENV, GOWORK, GOFLAGS, GOPACKAGESDRIVER, GOTOOLCHAIN, XDG_CACHE_HOME,
// XDG_STATE_HOME) cannot steer tests across packages while keeping Go's
// content-addressed build cache warm across temporary XDG cache homes.
func NeutralAmbient(tb TB) {
	tb.Helper()
	if os.Getenv("GOCACHE") == "" {
		if gocache := GoBuildCache(); gocache != "" {
			tb.Setenv("GOCACHE", gocache)
		}
	}
	tb.Setenv("XDG_CACHE_HOME", tb.TempDir())
	tb.Setenv("XDG_STATE_HOME", tb.TempDir())
	tb.Setenv("GOENV", "off")
	tb.Setenv("GOWORK", "off")
	tb.Setenv("GOFLAGS", "")
	tb.Setenv("GOPACKAGESDRIVER", "")
	tb.Setenv("GOTOOLCHAIN", "local")
}
