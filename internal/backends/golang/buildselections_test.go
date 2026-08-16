package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func buildSelectionModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/tagged\n\ngo 1.24\n",
		"p.go":   "package tagged\n\nfunc Plain(x int) int { return x }\n",
		"dst_test.go": `//go:build dst

package tagged

import "testing"

func TestCrashSchedule(t *testing.T) {
	if Plain(1) != 1 {
		t.Fatal()
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func dstPolicy(t *testing.T, dir string) {
	t.Helper()
	dstCfg := &stipulatorv1.GoInvocationConfig{}
	dstCfg.SetPackages([]string{"./..."})
	dstCfg.SetTags([]string{"dst"})
	plainCfg := &stipulatorv1.GoInvocationConfig{}
	plainCfg.SetPackages([]string{"./..."})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("all", plainCfg),
		goInvocation("dst", dstCfg),
	})
	writePolicyRecord(t, dir, p)
}

// A symbol declared only under a build tag the accepted policy runs
// resolves, shape-hashes, and reports its declaring file - without the
// policy record the same reference is NotFound, never a silent
// default-view narrowing (REQ-go-build-selections).
func TestResolveSpansPolicyBuildSelections(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := buildSelectionModule(t)
	symbol := "example.com/tagged.TestCrashSchedule"

	bare, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res, _, err := bare.Resolve(symbol); err != nil || res != verify.NotFound {
		t.Fatalf("tag-gated symbol without a policy = %v, %v; want NotFound", res, err)
	}

	dstPolicy(t, dir)
	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	res, shape, err := b.Resolve(symbol)
	if err != nil || res != verify.Resolved || shape == "" {
		t.Fatalf("tag-gated symbol = %v shape %q, %v; want Resolved with a shape hash", res, shape, err)
	}
	file, ok := b.SymbolFile(symbol)
	if !ok || file != "dst_test.go" {
		t.Fatalf("SymbolFile = %q %v, want the tag-gated declaring file", file, ok)
	}
	if res, _, err := b.Resolve("example.com/tagged.Plain"); err != nil || res != verify.Resolved {
		t.Fatalf("untagged symbol regressed: %v, %v", res, err)
	}
}

// When complementary build tags declare the same name, the default view
// wins: resolution and the shape hash come from the first-declaring
// view, deterministically (REQ-go-build-selections).
func TestResolveFirstDeclaringViewWins(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/dual\n\ngo 1.24\n",
		"plain.go": `//go:build !dst

package dual

func Shape(x int) int { return x }
`,
		"dst.go": `//go:build dst

package dual

func Shape(x string) string { return x }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dstPolicy(t, dir)
	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	res, shape, err := b.Resolve("example.com/dual.Shape")
	if err != nil || res != verify.Resolved {
		t.Fatalf("dual symbol = %v, %v", res, err)
	}
	// The control module carries only the default view's declaration:
	// its shape hash is the winner's expected hash.
	control := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":   "module example.com/dual\n\ngo 1.24\n",
		"plain.go": "package dual\n\nfunc Shape(x int) int { return x }\n",
	} {
		if err := os.WriteFile(filepath.Join(control, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cb, err := newContext(context.Background(), control)
	if err != nil {
		t.Fatal(err)
	}
	_, controlShape, err := cb.Resolve("example.com/dual.Shape")
	if err != nil || controlShape == "" {
		t.Fatal(err)
	}
	if shape != controlShape {
		t.Fatalf("shape %q, want the default view's %q", shape, controlShape)
	}
	if file, ok := b.SymbolFile("example.com/dual.Shape"); !ok || file != "plain.go" {
		t.Fatalf("SymbolFile = %q %v, want the default view's file", file, ok)
	}
}

// A malformed policy record is a verification error at load, never a
// silent narrowing to the default view (REQ-go-build-selections).
func TestResolveRefusesMalformedPolicyRecord(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := buildSelectionModule(t)
	full := filepath.Join(dir, filepath.FromSlash(".stipulator/policy.textproto"))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("not a policy {"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newContext(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "resolution build selections") {
		t.Fatalf("err = %v, want the malformed-record refusal", err)
	}
}

// The generated-file verdict is judged in the declaring view: a
// generated untagged sibling never lends its verdict to a tag-gated
// symbol in a hand-written file, and the generated arm itself still
// fires for the generated declaration (REQ-go-build-selections,
// REQ-go-generated-detect). Repeated runs guard the cross-view
// FileSet-confusion regression, whose failure was order-dependent.
func TestGeneratedVerdictJudgedInDeclaringView(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/genview\n\ngo 1.24\n",
		"gen_test.go": "// Code generated by tool. DO NOT EDIT.\n\npackage genview\n\nimport \"testing\"\n\nfunc TestFromGenerator(t *testing.T) {}\n",
		"dst_test.go": `//go:build dst

package genview

import "testing"

func TestCrashSchedule(t *testing.T) {}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dstPolicy(t, dir)
	for range 100 {
		b, err := newContext(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if res, _, err := b.Resolve("example.com/genview.TestCrashSchedule"); err != nil || res != verify.Resolved {
			t.Fatalf("tag-gated hand-written symbol = %v, %v; want Resolved", res, err)
		}
		if res, _, err := b.Resolve("example.com/genview.TestFromGenerator"); err != nil || res != verify.GeneratedFile {
			t.Fatalf("generated symbol = %v, %v; want GeneratedFile", res, err)
		}
	}
}

// Two views reaching the same named fact yield one canonical slice row
// - the first-reached declaration - never duplicate or conflicting
// rows (REQ-go-slice, REQ-go-build-selections).
func TestSliceDedupesAcrossViews(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/sliced\n\ngo 1.24\n",
		"config.go": "package sliced\n\ntype Config struct{ N int }\n\nfunc Plain(c Config) int { return c.N }\n",
		"dst.go": `//go:build dst

package sliced

func Tagged(c Config) int { return c.N * 2 }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dstPolicy(t, dir)
	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	decls, err := b.Slice([]string{"example.com/sliced.Plain", "example.com/sliced.Tagged"})
	if err != nil {
		t.Fatal(err)
	}
	configs := 0
	for _, d := range decls {
		if d.Name == "Config" {
			configs++
		}
	}
	if configs != 1 {
		t.Fatalf("Config rows = %d, want exactly one canonical row: %+v", configs, decls)
	}
}

// Between two tagged views, the policy's canonical invocation order
// decides the declaring view (REQ-go-build-selections).
func TestTaggedViewPrecedenceFollowsPolicyOrder(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/order\n\ngo 1.24\n",
		"p.go":   "package order\n\nfunc Anchor() {}\n",
		"a.go": `//go:build alpha

package order

func Which(x int) int { return x }
`,
		"b.go": `//go:build beta

package order

func Which(x string) string { return x }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	alphaCfg := &stipulatorv1.GoInvocationConfig{}
	alphaCfg.SetPackages([]string{"./..."})
	alphaCfg.SetTags([]string{"alpha"})
	betaCfg := &stipulatorv1.GoInvocationConfig{}
	betaCfg.SetPackages([]string{"./..."})
	betaCfg.SetTags([]string{"beta"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("a-alpha", alphaCfg),
		goInvocation("b-beta", betaCfg),
	})
	writePolicyRecord(t, dir, p)
	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if file, ok := b.SymbolFile("example.com/order.Which"); !ok || file != "a.go" {
		t.Fatalf("SymbolFile = %q %v, want the first tagged view's declaration", file, ok)
	}
}

// A view's identity is the invocation's tag-set AND toolchain: the
// selection's view loads under its own toolchain (a valid one
// resolves; a broken one proves the env reached the load), an
// unloadable tagged view degrades to a named refusal instead of
// failing the whole binding context, and a reference the loaded views
// cannot answer refuses with the degraded view named - never a silent
// NotFound (REQ-go-build-selections).
func TestTaggedViewLoadsUnderSelectionToolchain(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := buildSelectionModule(t)
	write := func(toolchain string) {
		t.Helper()
		dstCfg := &stipulatorv1.GoInvocationConfig{}
		dstCfg.SetPackages([]string{"./..."})
		dstCfg.SetTags([]string{"dst"})
		dstCfg.SetToolchain(toolchain)
		plainCfg := &stipulatorv1.GoInvocationConfig{}
		plainCfg.SetPackages([]string{"./..."})
		p := &stipulatorv1.TestPolicy{}
		p.SetInvocations([]*stipulatorv1.PolicyInvocation{
			goInvocation("all", plainCfg),
			goInvocation("dst", dstCfg),
		})
		writePolicyRecord(t, dir, p)
	}

	write("local")
	b, err := newContext(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res, _, err := b.Resolve("example.com/tagged.TestCrashSchedule"); err != nil || res != verify.Resolved {
		t.Fatalf("valid-toolchain dst view = %v, %v; want Resolved", res, err)
	}

	write("definitely-not-a-toolchain")
	b, err = newContext(context.Background(), dir)
	if err != nil {
		t.Fatalf("a broken tagged view failed the whole binding context: %v", err)
	}
	if res, _, err := b.Resolve("example.com/tagged.Plain"); err != nil || res != verify.Resolved {
		t.Fatalf("healthy remainder lost: %v, %v", res, err)
	}
	if _, _, err := b.Resolve("example.com/tagged.TestCrashSchedule"); err == nil || !strings.Contains(err.Error(), "build selection") {
		t.Fatalf("dst reference err = %v, want the degraded view named", err)
	}
}

// Two invocations sharing a tag-set under different toolchains are two
// distinct views (REQ-go-build-selections).
func TestPolicyBuildSelectionsSplitToolchains(t *testing.T) {
	stipulate.Covers(t, "REQ-go-build-selections")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/split\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &stipulatorv1.GoInvocationConfig{}
	a.SetPackages([]string{"./..."})
	a.SetTags([]string{"dst"})
	a.SetToolchain("go1.24.0")
	b := &stipulatorv1.GoInvocationConfig{}
	b.SetPackages([]string{"./..."})
	b.SetTags([]string{"dst"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{
		goInvocation("one", a),
		goInvocation("two", b),
	})
	writePolicyRecord(t, dir, p)
	selections, _, err := policyBuildSelections(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(selections) != 3 {
		t.Fatalf("selections = %+v, want default + two distinct (tags, toolchain) views", selections)
	}
	if selections[1].toolchain != "go1.24.0" || selections[2].toolchain != "" {
		t.Fatalf("selections = %+v, want toolchains split in policy order", selections)
	}
}
