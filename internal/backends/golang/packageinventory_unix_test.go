//go:build unix

package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestUnreadableLongerPackageNeverResolvesAShorterMethod(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a directory the current uid cannot read")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-zero directory; the failure distinction is pinned separately")
	}
	stipulate.Covers(t, "REQ-go-symbol", "REQ-go-static-binding")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.net/blocked\n\ngo 1.27\n",
		"p/p.go":    "package p\ntype v2 struct{}\nfunc (v2) F() int { return 1 }\n",
		"p.v2/p.go": "package p\nfunc F() string { return \"long\" }\n",
	})
	unreadable := filepath.Join(dir, "p.v2")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o755) })
	const symbol = "example.net/blocked/p.v2.F"
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, _, err := s.Resolve(symbol)
	if err == nil || res == verify.Resolved || !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), "p.v2") {
		t.Fatalf("unreadable longer candidate: %v %v", res, err)
	}
}
