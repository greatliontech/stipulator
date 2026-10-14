package golang

import (
	"context"
	"encoding/hex"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/go-x-tools/go/packages"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The inventory needs an explicit answer for each candidate: a cleanly
// truncated JSON stream is not evidence that the longer package is absent.
func TestDeclarationInventoryRequiresEveryAnswer(t *testing.T) {
	stipulate.Covers(t, "REQ-go-symbol", "REQ-evidence-resolution-freshness-typed-load")
	expected := map[string]string{"example.com/m": "example.com/m", "./m": "example.com/m", "example.com/m.v2": "example.com/m.v2", "./m.v2": "example.com/m.v2"}
	const short = `{"ImportPath":"example.com/m","Dir":"/tree/m","GoFiles":["m.go"]}`
	const long = `{"ImportPath":"example.com/m.v2","Dir":"/tree/m.v2","TestGoFiles":["m_test.go"]}`
	for _, row := range []struct {
		name, data string
		want       int
		fail       bool
	}{
		{"both", short + long, 2, false},
		{"cgo source", short + `{"ImportPath":"example.com/m.v2","Dir":"/tree/m.v2","CgoFiles":["m.go"]}`, 2, false},
		{"absent", short + `{"ImportPath":"./m.v2","Error":{"Err":"stat /tree/m.v2: directory not found"}}`, 1, false},
		{"no source", short + `{"ImportPath":"./m.v2","Dir":"/tree/m.v2","Error":{"Err":"no Go files in /tree/m.v2"}}`, 1, false},
		{"constraints", short + `{"ImportPath":"./m.v2","Dir":"/tree/m.v2","Error":{"Err":"build constraints exclude all Go files in /tree/m.v2"}}`, 1, false},
		{"other module", short + `{"ImportPath":"./m.v2","Error":{"Err":"main module (example.com/m) does not contain package example.com/m.v2"}}`, 1, false},
		{"invalid file retained", short + `{"ImportPath":"example.com/m.v2","Dir":"/tree/m.v2","InvalidGoFiles":["m.go"],"Error":{"Err":"syntax error"}}`, 2, false},
		{"missing", short, 0, true},
		{"empty", "", 0, true},
		{"duplicate", short + short + long, 0, true},
		{"unknown", short + `{"ImportPath":"foreign/pkg"}`, 0, true},
		{"torn", short + `{"ImportPath":`, 0, true},
		{"trailing", short + long + "garbage", 0, true},
		{"null", short + "null", 0, true},
		{"missing fact", short + `{"ImportPath":"./m.v2"}`, 0, true},
		{"permission", short + `{"ImportPath":"./m.v2","Error":{"Err":"stat /tree/directory not found/m.v2: permission denied"}}`, 0, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := readDeclarationPackages(t.Context(), []byte(row.data), expected)
			if (err != nil) != row.fail || len(got) != row.want {
				t.Fatalf("inventory=%v err=%v, want %d and failure=%t", got, err, row.want, row.fail)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readDeclarationPackages(ctx, []byte(short+long), expected); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled inventory: %v", err)
	}
}

func FuzzDottedSymbolCandidates(f *testing.F) {
	f.Add([]byte(""), uint8(0), false)
	f.Add([]byte("a"), uint8(1), false)
	f.Add([]byte("b"), uint8(2), true)
	f.Fuzz(func(t *testing.T, name []byte, dots uint8, method bool) {
		stipulate.Covers(t, "REQ-go-symbol")
		base := "example.com/p" + hex.EncodeToString(name)
		pkg := base + strings.Repeat(".v2", int(dots))
		symbol := pkg + ".F"
		if method {
			symbol = pkg + ".T.F"
		}
		candidates := symbolPackageCandidates(symbol)
		if len(candidates) > 2 || !slices.Contains(candidates, pkg) {
			t.Fatalf("%q lost owning package %q: %v", symbol, pkg, candidates)
		}
		backend := &Served{inventory: map[string]map[string]bool{"a": {pkg: true}}}
		if got, ok := backend.packageOf(symbol); !ok || got != pkg {
			t.Fatalf("declared owner=%q,%t, want %q", got, ok, pkg)
		}
		if method {
			backend.inventory["b"] = map[string]bool{pkg + ".T": true}
			if got, ok := backend.packageOf(symbol); !ok || got != pkg+".T" {
				t.Fatalf("longer function package lost: %q %t", got, ok)
			}
		} else if dots > 0 {
			backend.inventory["b"] = map[string]bool{base + strings.Repeat(".v2", int(dots)-1): true}
			if got, ok := backend.packageOf(symbol); !ok || got != pkg {
				t.Fatalf("shorter receiver package won: %q %t", got, ok)
			}
		}
	})
}

func TestEmptyDeclaredScopeNeverLoadsTheWholeTree(t *testing.T) {
	if testing.Short() {
		t.Skip("asks the actual scoped resolver child")
	}
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-typed-load")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/empty\n\ngo 1.27\n", "p.go": "package empty\nfunc Existing() {}\n"})
	s, err := NewServed(t.Context(), dir, []string{"not-a-symbol", "example.com/empty/missing.F"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if res, _, err := s.Resolve("not-a-symbol"); err != nil || res != verify.NotFound {
		t.Fatalf("invalid symbol: %v %v", res, err)
	}
	child, err := s.ensureChild()
	if err != nil {
		t.Fatal(err)
	}
	if res, _, _, err := child.ResolveIn("example.com/empty.Existing"); err != nil || res != verify.NotFound {
		t.Fatalf("empty child widened: %v %v", res, err)
	}
	whole, err := NewWholeTree(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	if res, _, err := whole.Resolve("example.com/empty.Existing"); err != nil || res != verify.Resolved {
		t.Fatalf("whole-tree control: %v %v", res, err)
	}
}

// A failed metadata pass cannot serve or publish a guessed package owner.
// The typed fallback asks only the syntactic candidates, not the whole tree.
func TestInventoryFailureUsesOnlyBoundedTypedCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("publishes a record and fails only the metadata runner")
	}
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-typed-load", "REQ-go-symbol")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":         "module example.com/fallback\n\ngo 1.27\n",
		"p/p.go":         "package p\ntype v2 struct{}\nfunc (v2) F() int { return 1 }\n",
		"p.v2/p.go":      "package p\nfunc F() string { return \"long\" }\n",
		"unrelated/u.go": "package unrelated\nfunc Other() {}\n",
	})
	const symbol = "example.com/fallback/p.v2.F"
	first, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	res, shape, err := first.Resolve(symbol)
	if err != nil || res != verify.Resolved {
		t.Fatalf("initial resolution: %v %v", res, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	original := ownedRunner
	t.Cleanup(func() { ownedRunner = original })
	absent := filepath.Join(t.TempDir(), "absent-command")
	ownedRunner.Prepare = func(cmd *exec.Cmd) {
		if original.Prepare != nil {
			original.Prepare(cmd)
		}
		for _, arg := range cmd.Args {
			if strings.HasPrefix(arg, "-json=ImportPath,Dir,GoFiles,") {
				cmd.Path = absent
			}
		}
	}
	failed, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer failed.Close()
	if failed.ServedCount() != 0 || failed.inventoryComplete() {
		t.Fatal("failed inventory authorized serving")
	}
	if res, got, err := failed.Resolve(symbol); err != nil || res != verify.Resolved || got != shape {
		t.Fatalf("typed fallback: %v %q %v", res, got, err)
	}
	if res, _, _, err := failed.child.ResolveIn("example.com/fallback/unrelated.Other"); err != nil || res != verify.NotFound {
		t.Fatalf("typed fallback widened: %v %v", res, err)
	}
	if len(failed.opening) != 0 {
		t.Fatalf("failed inventory acquired opening evidence: %v", failed.opening)
	}
	if err := failed.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(failed.Notices(), "\n"), "package inventory") {
		t.Fatalf("inventory failure unnamed: %v", failed.Notices())
	}
	ownedRunner = original
	again, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.ServedCount() != 1 {
		t.Fatalf("failure destroyed the valid prior record: %v", again.Notices())
	}
}

func TestTypedPackageAbsenceRequiresExplicitEvidence(t *testing.T) {
	stipulate.Covers(t, "REQ-go-static-binding", "REQ-go-symbol")
	for _, row := range []struct {
		errors []string
		keep   bool
	}{
		{[]string{"no required module provides package example.com/absent"}, false},
		{[]string{"no Go files in /tree/absent"}, false},
		{[]string{"permission denied"}, true},
		{[]string{"stat /tree/directory not found/p: permission denied"}, true},
		{[]string{"no Go files in /tree/absent", "permission denied"}, true},
		{[]string{"go command failed"}, true},
	} {
		pkg := &packages.Package{PkgPath: "example.com/p.v2"}
		for _, msg := range row.errors {
			pkg.Errors = append(pkg.Errors, packages.Error{Msg: msg})
		}
		got := matchedRoots([]*packages.Package{pkg})
		if (len(got) == 1) != row.keep {
			t.Fatalf("errors %v: roots=%v, keep=%t", row.errors, got, row.keep)
		}
		if row.keep && got[0] != pkg {
			t.Fatal("failed root replaced instead of retained")
		}
	}
}

func TestCgoOnlyPackageIsADeclarationInventoryMember(t *testing.T) {
	if testing.Short() {
		t.Skip("asks Go's actual cgo file inventory")
	}
	stipulate.Covers(t, "REQ-go-symbol")
	neutralAmbient(t)
	t.Setenv("CGO_ENABLED", "1")
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.net/cgopkg\n\ngo 1.27\n",
		"p.go":   "package cgopkg\nimport \"C\"\nfunc F() int { return 1 }\n",
	})
	s, err := NewServed(t.Context(), dir, []string{"example.net/cgopkg.F"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.inventoryComplete() || !s.inventory["default"]["example.net/cgopkg"] {
		t.Fatalf("cgo declaration lost: %v; %v", s.inventory, s.Notices())
	}
}
