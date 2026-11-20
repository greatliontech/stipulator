package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(IsolateMain(m))
}

func TestIsolateMainAndNeutralAmbientPreserveGoCache(t *testing.T) {
	if os.Getenv("TESTENV_CHILD_CHECK") == "1" {
		fmt.Println("XDG_CACHE_HOME=" + os.Getenv("XDG_CACHE_HOME"))
		return
	}
	suiteCache := os.Getenv("XDG_CACHE_HOME")
	if !filepath.IsAbs(suiteCache) {
		t.Fatalf("suite XDG_CACHE_HOME = %q, want isolated absolute path", suiteCache)
	}
	suiteState := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(suiteState) {
		t.Fatalf("suite XDG_STATE_HOME = %q, want isolated absolute path", suiteState)
	}
	wantGoCache := GoBuildCache()
	if !filepath.IsAbs(wantGoCache) {
		t.Fatalf("GoBuildCache() = %q, want absolute path", wantGoCache)
	}
	t.Setenv("GOCACHE", "")
	NeutralAmbient(t)
	if got := os.Getenv("GOCACHE"); got != wantGoCache {
		t.Fatalf("GOCACHE after NeutralAmbient = %q, want %q", got, wantGoCache)
	}
	perTestCache := os.Getenv("XDG_CACHE_HOME")
	if perTestCache == "" || perTestCache == suiteCache {
		t.Fatalf("XDG_CACHE_HOME after NeutralAmbient = %q, want per-test temp dir distinct from %q", perTestCache, suiteCache)
	}
	if got := os.Getenv("GOENV"); got != "off" {
		t.Fatalf("GOENV = %q, want off", got)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestIsolateMainAndNeutralAmbientPreserveGoCache$", "-test.count=1")
	child.Env = append(os.Environ(), "TESTENV_CHILD_CHECK=1")
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("self-exec child failed: %s %v", out, err)
	}
	if !strings.Contains(string(out), "XDG_CACHE_HOME="+perTestCache) {
		t.Fatalf("self-exec child output = %q, want inherited XDG_CACHE_HOME=%s", out, perTestCache)
	}
}
