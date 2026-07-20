package facts

import (
	"strings"
	"testing"
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// Two spec islands: a/b/new are connected (new is greenfield, unbound, but
// depends on a); lone floats alone with its own binding.
const doc = "# T\n\n" +
	"**REQ-f-a** (behavior): It MUST x.\n\n" +
	"**REQ-f-b** (behavior, refines REQ-f-a): It MUST y.\n\n" +
	"**REQ-f-new** (behavior, depends REQ-f-a): Greenfield, it MUST z.\n\n" +
	"**REQ-f-lone** (behavior): Alone it MUST w.\n"

const bindings = `bindings {
  requirement_id: "REQ-f-a"
  backend: "go"
  symbol: "example.com/p.ImplA"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-f-b"
  backend: "go"
  symbol: "example.com/p.ImplB"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-f-lone"
  backend: "go"
  symbol: "example.com/q.ImplLone"
  role: BINDING_ROLE_IMPLEMENTS
}
`

func fixture(t *testing.T) (*stipulatorv1.Spec, *records.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto":   {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                       {Data: []byte(doc)},
		".stipulator/bindings/f.textproto": {Data: []byte(bindings)},
	}
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatalf("compile: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return spec, store
}

// fakeSlicer maps symbols to packages, one Decl per symbol.
type fakeSlicer map[string]string

func (f fakeSlicer) Resolve(symbol string) (verify.Resolution, string, error) {
	return verify.Resolved, strings.Repeat("s", 64), nil
}

func (f fakeSlicer) Slice(symbols []string) ([]verify.Decl, error) {
	var out []verify.Decl
	for _, sym := range symbols {
		if pkg, ok := f[sym]; ok {
			out = append(out, verify.Decl{Package: pkg, Name: sym, Declaration: "func " + sym, ShapeHash: strings.Repeat("d", 64)})
		}
	}
	return out, nil
}

var slicer = fakeSlicer{
	"example.com/p.ImplA":    "example.com/p",
	"example.com/p.ImplB":    "example.com/p",
	"example.com/q.ImplLone": "example.com/q",
}

func backends() map[string]verify.Backend {
	return map[string]verify.Backend{"go": slicer}
}

// TestSeedsFromNeighborhood pins the greenfield story: REQ-f-new has no
// bindings, but seeding it reaches its dependency's bindings.
//
//gofresh:pure
func TestSeedsFromNeighborhood(t *testing.T) {
	stipulate.Covers(t, "REQ-context-seeds")
	spec, store := fixture(t)
	seeds, err := Seeds(spec, store, []string{"REQ-f-new"})
	if err != nil {
		t.Fatal(err)
	}
	var syms []string
	for _, s := range seeds {
		syms = append(syms, s.Symbol)
	}
	got := strings.Join(syms, " ")
	if !strings.Contains(got, "example.com/p.ImplA") {
		t.Fatalf("greenfield seeding missed the neighborhood: %s", got)
	}
	if strings.Contains(got, "ImplLone") {
		t.Fatalf("seeding leaked an unrelated island: %s", got)
	}

	if _, err := Seeds(spec, store, []string{"REQ-f-ghost"}); err == nil {
		t.Fatal("unknown requirement accepted")
	}
}

//gofresh:pure
func TestContextSlices(t *testing.T) {
	spec, store := fixture(t)
	seeds, decls, err := Context(spec, store, backends(), []string{"REQ-f-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) == 0 || len(decls) == 0 {
		t.Fatalf("seeds=%d decls=%d", len(seeds), len(decls))
	}
	for _, d := range decls {
		if d.Package == "" || d.ShapeHash == "" {
			t.Fatalf("decl not fact-complete: %+v", d)
		}
	}
}

// TestPartitions pins connectivity and disjointness: a/b/new form one
// component (intersecting closures), lone another; their packages differ,
// so no overlap is reported.
//
//gofresh:pure
func TestPartitions(t *testing.T) {
	stipulate.Covers(t, "REQ-context-partitions")
	spec, store := fixture(t)
	rep, err := Partitions(spec, store, backends(), []string{"REQ-f-a", "REQ-f-b", "REQ-f-new", "REQ-f-lone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Components) != 2 {
		t.Fatalf("components = %d, want 2: %+v", len(rep.Components), rep.Components)
	}
	sizes := []int{len(rep.Components[0].Requirements), len(rep.Components[1].Requirements)}
	if !(sizes[0] == 3 && sizes[1] == 1 || sizes[0] == 1 && sizes[1] == 3) {
		t.Fatalf("component sizes = %v", sizes)
	}
	if len(rep.Overlaps) != 0 {
		t.Fatalf("disjoint packages reported overlapping: %+v", rep.Overlaps)
	}

	// Force an overlap: bind lone into package p as well.
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
		".stipulator/bindings/f.textproto": {Data: []byte(bindings + `bindings {
  requirement_id: "REQ-f-lone"
  backend: "go"
  symbol: "example.com/p.ImplA"
  role: BINDING_ROLE_TESTS
}
`)},
	}
	spec2, diags, err := compile.Compile(fsys)
	if err != nil || len(diags) > 0 {
		t.Fatal(err, diags)
	}
	store2, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	rep2, err := Partitions(spec2, store2, backends(), []string{"REQ-f-a", "REQ-f-lone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Overlaps) != 1 || rep2.Overlaps[0].Packages[0] != "example.com/p" {
		t.Fatalf("shared package not reported: %+v", rep2.Overlaps)
	}
}

// The wire overlap list is capped at the heaviest couplings with the
// remainder counted — a silent truncation would read as "no more
// overlaps" (REQ-mcp-response-contract).
//
//gofresh:pure
func TestPartitionProtoCapsOverlaps(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-response-contract")
	r := &Report{}
	for i := 0; i < OverlapCap+9; i++ {
		pkgs := []string{"pkg/shared"}
		if i == 0 {
			// The heaviest coupling must survive the cap.
			pkgs = []string{"pkg/a", "pkg/b", "pkg/c"}
		}
		r.Overlaps = append(r.Overlaps, Overlap{A: i, B: i + 1, Packages: pkgs})
	}
	m := r.Proto()
	if len(m.GetOverlaps()) != OverlapCap {
		t.Fatalf("overlaps = %d, want the cap %d", len(m.GetOverlaps()), OverlapCap)
	}
	if m.GetOverlapsOmitted() != 9 {
		t.Fatalf("omitted = %d, want 9", m.GetOverlapsOmitted())
	}
	if len(m.GetOverlaps()[0].GetPackages()) != 3 {
		t.Fatal("the heaviest coupling did not rank first")
	}
	// The export form is uncapped: everything travels, nothing omitted.
	full := r.ProtoUncapped()
	if len(full.GetOverlaps()) != OverlapCap+9 || full.GetOverlapsOmitted() != 0 {
		t.Fatalf("uncapped form = %d overlaps, %d omitted", len(full.GetOverlaps()), full.GetOverlapsOmitted())
	}
}
