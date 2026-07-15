package bindingsurface

import (
	"math/rand"
	"slices"
	"strings"
	"testing"

	surfacewire "github.com/greatliontech/stipulator/bindingsurface"
	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
)

func TestDeriveCanonicalSurfaces(t *testing.T) {
	spec := testSpec("REQ-a", "REQ-b", "REQ-c", "REQ-d")
	store := testStore(
		binding("REQ-b", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-d", "go", "p.A", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-a", "go", "p.TestZ", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-a", "go", "p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-a", "go", "p.TestA", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-a", "proto", "p.TestProto", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-b", "go", "p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-b", "proto", "p.Proof", stipulatorv1.BindingRole_BINDING_ROLE_PROVES),
		binding("REQ-c", "proto", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
	)

	report, err := Derive(spec, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := surfacewire.Validate(report); err != nil {
		t.Fatalf("derived report is not valid shared wire data: %v", err)
	}
	if report.GetFormat() != surfacewire.Format || len(report.GetSurfaces()) != 3 {
		t.Fatalf("report = %v", report)
	}
	goA, goSurface, protoSurface := report.GetSurfaces()[0], report.GetSurfaces()[1], report.GetSurfaces()[2]
	if goA.GetBackend() != "go" || goA.GetSymbol() != "p.A" ||
		!slices.Equal(goA.GetRequirementIds(), []string{"REQ-d"}) || len(goA.GetBindings()) != 0 {
		t.Fatalf("same-backend surface order = %v", report.GetSurfaces())
	}
	if goSurface.GetBackend() != "go" || goSurface.GetSymbol() != "p.F" ||
		!slices.Equal(goSurface.GetRequirementIds(), []string{"REQ-a", "REQ-b"}) {
		t.Fatalf("go surface = %v", goSurface)
	}
	bindings := goSurface.GetBindings()
	var gotBindings []string
	for _, binding := range bindings {
		gotBindings = append(gotBindings, binding.GetRole().String()+"|"+binding.GetBackend()+"|"+binding.GetSymbol())
	}
	wantBindings := []string{
		"BINDING_ROLE_TESTS|go|p.TestA",
		"BINDING_ROLE_TESTS|go|p.TestShared",
		"BINDING_ROLE_TESTS|go|p.TestZ",
		"BINDING_ROLE_TESTS|proto|p.TestProto",
		"BINDING_ROLE_PROVES|proto|p.Proof",
	}
	if !slices.Equal(gotBindings, wantBindings) {
		t.Fatalf("associated bindings = %v", bindings)
	}
	if protoSurface.GetBackend() != "proto" || protoSurface.GetSymbol() != "p.F" ||
		!slices.Equal(protoSurface.GetRequirementIds(), []string{"REQ-c"}) || len(protoSurface.GetBindings()) != 0 {
		t.Fatalf("witness-less surface = %v", protoSurface)
	}
}

func TestSurfaceIdentifierTracksOnlyRepresentedRelationship(t *testing.T) {
	stipulate.Covers(t, "REQ-advisory-surface-id")
	spec := testSpec("REQ-a", "REQ-b")
	baseBindings := []*stipulatorv1.Binding{
		binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-b", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-a", "go", "p.TestF", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	}
	base, err := Derive(spec, testStore(baseBindings...))
	if err != nil {
		t.Fatal(err)
	}
	baseID := base.GetSurfaces()[0].GetId()

	duplicateProjection := append(slices.Clone(baseBindings),
		binding("REQ-b", "go", "p.TestF", stipulatorv1.BindingRole_BINDING_ROLE_TESTS))
	projected, err := Derive(spec, testStore(duplicateProjection...))
	if err != nil || projected.GetSurfaces()[0].GetId() != baseID {
		t.Fatalf("duplicate projection moved identifier: %v %v", projected, err)
	}

	pinned := proto.Clone(baseBindings[0]).(*stipulatorv1.Binding)
	pinned.SetContentHash(strings.Repeat("a", 64))
	pinned.SetShapeHash(strings.Repeat("b", 64))
	pinChanged := append([]*stipulatorv1.Binding{pinned}, baseBindings[1:]...)
	stable, err := Derive(spec, testStore(pinChanged...))
	if err != nil || stable.GetSurfaces()[0].GetId() != baseID {
		t.Fatalf("pin change moved identifier: %v %v", stable, err)
	}

	unique := append(slices.Clone(baseBindings),
		binding("REQ-a", "go", "p.Other", stipulatorv1.BindingRole_BINDING_ROLE_TESTS))
	changed, err := Derive(spec, testStore(unique...))
	if err != nil || changed.GetSurfaces()[0].GetId() == baseID {
		t.Fatalf("unique binding did not move identifier: %v %v", changed, err)
	}

	withoutRequirement, err := Derive(spec, testStore(baseBindings[0], baseBindings[2]))
	if err != nil || withoutRequirement.GetSurfaces()[0].GetId() == baseID {
		t.Fatalf("requirement removal did not move identifier: %v %v", withoutRequirement, err)
	}
}

func TestDeriveProjectsSharedRequirementOntoEveryImplementation(t *testing.T) {
	report, err := Derive(testSpec("REQ-shared"), testStore(
		binding("REQ-shared", "go", "p.A", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-shared", "go", "p.B", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-shared", "go", "p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.GetSurfaces()) != 2 {
		t.Fatalf("surfaces = %v, want two implementations", report.GetSurfaces())
	}
	for _, surface := range report.GetSurfaces() {
		if !slices.Equal(surface.GetRequirementIds(), []string{"REQ-shared"}) ||
			len(surface.GetBindings()) != 1 ||
			surface.GetBindings()[0].GetSymbol() != "p.TestShared" {
			t.Fatalf("shared requirement projection = %v", surface)
		}
	}
}

func TestFilterSelectsWholeStableSurfaces(t *testing.T) {
	report, err := Derive(testSpec("REQ-a", "REQ-b"), testStore(
		binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-b", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-b", "proto", "p.G", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-a", "go", "p.TestF", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	goID := report.GetSurfaces()[0].GetId()

	filtered, err := Filter(report, []string{"REQ-a", "REQ-absent"}, []string{"go"}, []string{"p.F"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.GetSurfaces()) != 1 || filtered.GetSurfaces()[0].GetId() != goID ||
		!proto.Equal(filtered.GetSurfaces()[0], report.GetSurfaces()[0]) {
		t.Fatalf("filtered report changed its surface: %v", filtered)
	}
	symbolOnly, err := Filter(report, nil, nil, []string{"p.G"})
	if err != nil || len(symbolOnly.GetSurfaces()) != 1 || symbolOnly.GetSurfaces()[0].GetSymbol() != "p.G" {
		t.Fatalf("symbol-only filter = %v, %v", symbolOnly, err)
	}
	if _, err := Filter(report, []string{"REQ-a"}, []string{"proto"}, nil); err == nil {
		t.Fatal("intersecting filters with no match succeeded")
	}
	emptyReport := &surfacewire.Report{}
	emptyReport.SetFormat(surfacewire.Format)
	emptyReport.SetSurfaces([]*surfacewire.Surface{})
	empty, err := Filter(emptyReport, nil, nil, nil)
	if err != nil || len(empty.GetSurfaces()) != 0 {
		t.Fatalf("unfiltered empty report = %v, %v", empty, err)
	}
}

func TestDeriveEmptyReport(t *testing.T) {
	report, err := Derive(testSpec("REQ-a"), testStore(
		binding("REQ-a", "go", "p.TestA", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil || report.GetFormat() != surfacewire.Format || len(report.GetSurfaces()) != 0 {
		t.Fatalf("empty report = %v, %v", report, err)
	}
}

func TestDeriveIsPermutationInvariant(t *testing.T) {
	stipulate.Covers(t, "REQ-advisory-surface-id")
	spec := testSpec("REQ-a", "REQ-b")
	bindings := []*stipulatorv1.Binding{
		binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-b", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-a", "go", "p.TestA", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-b", "proto", "p.Proof", stipulatorv1.BindingRole_BINDING_ROLE_PROVES),
	}
	want, err := Derive(spec, testStore(bindings...))
	if err != nil {
		t.Fatal(err)
	}
	rapid.Check(t, func(rt *rapid.T) {
		shuffled := slices.Clone(bindings)
		rand.New(rand.NewSource(rapid.Int64().Draw(rt, "seed"))).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		split := rapid.IntRange(0, len(shuffled)).Draw(rt, "split")
		store := &records.Store{Bindings: []records.BindingFile{
			{Path: "z.textproto", Set: bindingSet(shuffled[:split]...)},
			{Path: "a.textproto", Set: bindingSet(shuffled[split:]...)},
		}}
		got, err := Derive(spec, store)
		if err != nil || !proto.Equal(got, want) {
			rt.Fatalf("permutation changed report: %v %v", got, err)
		}
	})
}

func TestDeriveRejectsIllFormedBindings(t *testing.T) {
	stipulate.Covers(t, "REQ-advisory-validation")
	valid := binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)
	duplicate := proto.Clone(valid).(*stipulatorv1.Binding)
	duplicate.SetContentHash(strings.Repeat("a", 64))
	tests := []struct {
		name     string
		bindings []*stipulatorv1.Binding
		want     string
	}{
		{"missing requirement", []*stipulatorv1.Binding{binding("", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)}, "without requirement_id"},
		{"missing backend", []*stipulatorv1.Binding{binding("REQ-a", "", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)}, "has no backend"},
		{"missing symbol", []*stipulatorv1.Binding{binding("REQ-a", "go", "", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)}, "has no symbol"},
		{"missing role", []*stipulatorv1.Binding{binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED)}, "unrecognized role 0"},
		{"unknown role", []*stipulatorv1.Binding{binding("REQ-a", "go", "p.F", stipulatorv1.BindingRole(99))}, "unrecognized role 99"},
		{"unknown requirement", []*stipulatorv1.Binding{binding("REQ-z", "go", "p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS)}, "not in the corpus"},
		{"duplicate", []*stipulatorv1.Binding{valid, duplicate}, "duplicate binding"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Derive(testSpec("REQ-a"), testStore(test.bindings...))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func testSpec(ids ...string) *stipulatorv1.Spec {
	spec := &stipulatorv1.Spec{}
	var requirements []*stipulatorv1.Requirement
	for _, id := range ids {
		requirement := &stipulatorv1.Requirement{}
		requirement.SetId(id)
		requirements = append(requirements, requirement)
	}
	spec.SetRequirements(requirements)
	return spec
}

func testStore(bindings ...*stipulatorv1.Binding) *records.Store {
	return &records.Store{Bindings: []records.BindingFile{{Path: "bindings.textproto", Set: bindingSet(bindings...)}}}
}

func bindingSet(bindings ...*stipulatorv1.Binding) *stipulatorv1.BindingSet {
	set := &stipulatorv1.BindingSet{}
	set.SetBindings(bindings)
	return set
}

func binding(id, backend, symbol string, role stipulatorv1.BindingRole) *stipulatorv1.Binding {
	binding := &stipulatorv1.Binding{}
	binding.SetRequirementId(id)
	binding.SetBackend(backend)
	binding.SetSymbol(symbol)
	binding.SetRole(role)
	return binding
}
