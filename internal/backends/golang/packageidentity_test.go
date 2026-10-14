package golang

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The same dotted spelling can name a function in a longer package path
// or a receiver method in a shorter one. Scoped and whole-tree readers
// must select the same declared package before capturing or publishing.
func TestDottedPackageIdentitySurvivesScopedServing(t *testing.T) {
	if testing.Short() {
		t.Skip("resolves and persists two dotted package interpretations")
	}
	stipulate.Covers(t, "REQ-go-symbol", "REQ-evidence-resolution-freshness-typed-load")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/dotted\n\ngo 1.27\n",
		"m/m.go":    "package m\nfunc F() int { return 1 }\ntype v2 struct{}\nfunc (v2) F() int { return 2 }\n",
		"m.v2/m.go": "package m\nfunc F() string { return \"long\" }\ntype T struct{}\nfunc (T) Method() int { return 3 }\n",
	})
	symbols := []string{"example.com/dotted/m.F", "example.com/dotted/m.v2.F", "example.com/dotted/m.v2.T.Method"}
	whole, err := newContext(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServed(t.Context(), dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, symbol := range symbols {
		wantRes, wantShape, err := whole.Resolve(symbol)
		if err != nil || wantRes != verify.Resolved {
			t.Fatalf("whole %s: %v %v", symbol, wantRes, err)
		}
		gotRes, gotShape, err := s.Resolve(symbol)
		if err != nil || gotRes != wantRes || gotShape != wantShape {
			t.Errorf("scoped %s: %v %q %v; want %v %q", symbol, gotRes, gotShape, err, wantRes, wantShape)
		}
		if pkg, err := s.SymbolPackage(symbol); err != nil || pkg != whole.SymbolPackage(symbol) {
			t.Errorf("scoped %s package %q: %v", symbol, pkg, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, record := range resolutioncache.Load(dir) {
		if record.Symbol == symbols[1] && record.Package != "example.com/dotted/m.v2" {
			t.Fatalf("published ambiguous package: %+v", record)
		}
	}
	second, err := NewServed(t.Context(), dir, symbols[:2])
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.ServedCount() != 2 {
		t.Fatalf("published functions did not serve: %d; %v; %v", second.ServedCount(), second.Reasons(), s.Notices())
	}
}

// A package may exist only in a declared tagged view. Its metadata must
// use that view before choosing between a dotted package and a receiver.
func TestDottedPackageInventoryUsesItsBuildSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("captures a package that exists only under a tag")
	}
	stipulate.Covers(t, "REQ-go-symbol", "REQ-go-build-selections")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/tagonly\n\ngo 1.27\n",
		"p/p.go":           "package p\ntype v2 struct{}\nfunc (v2) TestF() {}\n",
		"p.v2/tag_test.go": "//go:build chosen\n\npackage leaf\nimport \"testing\"\nfunc TestF(*testing.T) {}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./..."})
	cfg.SetTags([]string{"chosen"})
	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("chosen", cfg)})
	writePolicyRecord(t, dir, p)
	const symbol = "example.com/tagonly/p.v2.TestF"
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if res, _, key, err := s.ResolveIn(symbol); err != nil || res != verify.Resolved || key != "chosen\x00\x00\x00\x00" {
		t.Fatalf("tagged package resolution: %v %q %v", res, key, err)
	}
	if pkg, err := s.SymbolPackage(symbol); err != nil || pkg != "example.com/tagonly/p.v2" {
		t.Fatalf("tagged package owner: %q %v", pkg, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.ServedCount() != 1 {
		t.Fatalf("tagged source record did not serve: %v; %v", s.Notices(), again.Notices())
	}
}
