package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/stipulate"
)

// The slice's package-level sound floor: a blank import (the classic
// init-effect channel) appears as a widened row though no signature
// names it - at depth one and through a transitive hop - a
// signature-referenced package reads declared, a module dependency
// outside the module is an external row, the standard library stays
// out, and a real package whose path ends in "_test" keeps its own
// path - over-approximating, dispositioned, never a silent gap
// (REQ-go-slice's floor arm).
func TestGoSliceFloorDispositions(t *testing.T) {
	stipulate.Covers(t, "REQ-go-slice")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":             "module example.com/floored\n\ngo 1.24\n\nrequire pgregory.net/rapid v0.0.0\n\nreplace pgregory.net/rapid => ./rapidstub\n",
		"rapidstub/go.mod":   "module pgregory.net/rapid\n\ngo 1.24\n",
		"rapidstub/rapid.go": "package rapid\n\nfunc Check() {}\n",
		"effects/effects.go": "package effects\n\nfunc init() {}\n",
		"deep/deep.go":       "package deep\n\nfunc init() {}\n",
		"my_test/my.go":      "package mytest\n\nfunc init() {}\n",
		"my/my.go":           "package my\n\nfunc My() {}\n",
		"helpers/h.go":       "package helpers\n\nimport _ \"example.com/floored/app\"\n\nfunc H() {}\n",
		"app/helper_test.go":  "package app\n\nimport _ \"example.com/floored/helpers\"\n",
		"types/types.go":     "package types\n\nimport _ \"example.com/floored/deep\"\n\ntype Config struct{ N int }\n",
		"app/app.go":         "package app\n\nimport (\n\t\"fmt\"\n\n\t_ \"example.com/floored/effects\"\n\t_ \"example.com/floored/my_test\"\n\n\t\"pgregory.net/rapid\"\n\n\t\"example.com/floored/types\"\n)\n\nfunc Use(c types.Config) int { rapid.Check(); return len(fmt.Sprint(c.N)) }\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	floor, err := b.SliceFloor([]string{"example.com/floored/app.Use"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range floor {
		got[f.Package] = f.Disposition
	}
	if got["example.com/floored/types"] != "declared" {
		t.Fatalf("signature-referenced package = %q, want declared (%v)", got["example.com/floored/types"], floor)
	}
	if got["example.com/floored/effects"] != "widened" {
		t.Fatalf("blank-imported package = %q, want widened (%v)", got["example.com/floored/effects"], floor)
	}
	if got["example.com/floored/deep"] != "widened" {
		t.Fatalf("transitively-reached package = %q, want widened (%v)", got["example.com/floored/deep"], floor)
	}
	if got["example.com/floored/my_test"] != "widened" {
		t.Fatalf("real _test-suffixed package = %q, want widened under its own path even beside a real base package (%v)", got["example.com/floored/my_test"], floor)
	}
	if _, folded := got["example.com/floored/my"]; folded {
		t.Fatalf("unimported sibling base package appeared - _test path folded by spelling: %v", floor)
	}
	if got["example.com/floored/helpers"] != "widened" {
		t.Fatalf("test-channel dependency on a rebuilt-variant cycle = %q, want widened (%v)", got["example.com/floored/helpers"], floor)
	}
	for p := range got {
		if strings.Contains(p, " [") {
			t.Fatalf("bracketed build identity leaked into a floor row %q: %v", p, floor)
		}
	}
	if got["pgregory.net/rapid"] != "external" {
		t.Fatalf("module dependency = %q, want external (%v)", got["pgregory.net/rapid"], floor)
	}
	if _, leaked := got["fmt"]; leaked {
		t.Fatalf("standard library leaked into the floor: %v", floor)
	}
	for p, d := range got {
		if d == "external" && p != "pgregory.net/rapid" {
			t.Fatalf("unexpected external row %q (standard library must stay out): %v", p, floor)
		}
	}
}
