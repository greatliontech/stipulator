package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/backends/golang"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// Exercise the default CLI adapter itself: replacing its constructor with
// NewWholeTree must not pass merely because injected face tests preserve scope.
func TestCLIDefaultDeclarationAdapterKeepsExplicitScope(t *testing.T) {
	if testing.Short() {
		t.Skip("loads declarations through the production CLI adapter")
	}
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-typed-load")
	golang.NeutralAmbient(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/adapter\n\ngo 1.27\n",
		"p/p.go": "package p\nfunc Selected() int { return 1 }\n",
		"q/q.go": "package q\nfunc Outside() int { return 2 }\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const selected = "example.com/adapter/p.Selected"
	for _, tc := range []struct {
		name    string
		symbols []string
	}{
		{"selected", []string{selected}},
		{"nil is explicit empty", nil},
		{"empty", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backends, closeBackends, err := declarationBackends(t.Context(), dir, tc.symbols)
			if err != nil {
				t.Fatal(err)
			}
			defer closeBackends()
			be := backends["go"]
			if be == nil {
				t.Fatal("default adapter omitted Go backend")
			}
			if len(tc.symbols) != 0 {
				if res, shape, err := be.Resolve(selected); err != nil || res != verify.Resolved || shape == "" {
					t.Fatalf("selected declaration: %v %q %v", res, shape, err)
				}
			} else {
				// A malformed reference reaches the explicitly empty typed
				// child; a subsequent real declaration must remain refused.
				if res, _, err := be.Resolve("malformed"); err != nil || res != verify.NotFound {
					t.Fatalf("empty typed load: %v %v", res, err)
				}
				if _, _, err := be.Resolve(selected); err == nil || !strings.Contains(err.Error(), "outside this operation's symbol set") {
					t.Fatalf("empty scope admitted selected declaration: %v", err)
				}
			}
			if _, _, err := be.Resolve("example.com/adapter/q.Outside"); err == nil || !strings.Contains(err.Error(), "outside this operation's symbol set") {
				t.Fatalf("default adapter admitted outside declaration: %v", err)
			}
		})
	}
}
