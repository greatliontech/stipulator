package bindingsurface

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

func TestContractFixtures(t *testing.T) {
	full := fixtureFullReport(t)
	valid := map[string]*stipulatorv1.BindingSurfaceReport{
		"valid/full.json":          full,
		"valid/empty.json":         fixtureEmptyReport(t),
		"valid/mixed-backend.json": fixtureMixedBackendReport(t),
		"valid/unicode.json":       fixtureUnicodeReport(t),
	}
	for _, path := range sortedReportPaths(valid) {
		report := valid[path]
		assertReportFixture(t, path, report)
	}

	oneSurface := fixtureInvalidBaseReport(t)
	orderedSurfaces := fixtureSurfaceOrderReport(t)
	invalid := map[string]*stipulatorv1.BindingSurfaceReport{}
	mutate := func(path string, fn func(*stipulatorv1.BindingSurfaceReport)) {
		report := proto.Clone(oneSurface).(*stipulatorv1.BindingSurfaceReport)
		fn(report)
		invalid[path] = report
	}
	mutate("invalid/unknown-format.json", func(report *stipulatorv1.BindingSurfaceReport) {
		report.SetFormat("stipulator.binding-surfaces/v2")
	})
	mutate("invalid/bad-id.json", func(report *stipulatorv1.BindingSurfaceReport) {
		report.GetSurfaces()[0].SetId(strings.Repeat("0", 64))
	})
	mutateFull := func(path string, fn func(*stipulatorv1.BindingSurfaceReport)) {
		report := proto.Clone(orderedSurfaces).(*stipulatorv1.BindingSurfaceReport)
		fn(report)
		invalid[path] = report
	}
	mutateFull("invalid/noncanonical-surfaces.json", func(report *stipulatorv1.BindingSurfaceReport) {
		slices.Reverse(report.GetSurfaces())
	})
	mutateFull("invalid/duplicate-surface.json", func(report *stipulatorv1.BindingSurfaceReport) {
		surface := proto.Clone(report.GetSurfaces()[1]).(*stipulatorv1.BindingSurface)
		surfaces := report.GetSurfaces()
		report.SetSurfaces(append(surfaces[:2], append([]*stipulatorv1.BindingSurface{surface}, surfaces[2:]...)...))
	})
	mutateFull("invalid/duplicate-id.json", func(report *stipulatorv1.BindingSurfaceReport) {
		report.GetSurfaces()[1].SetId(report.GetSurfaces()[0].GetId())
	})
	mutateFull("invalid/duplicate-surface-key.json", func(report *stipulatorv1.BindingSurfaceReport) {
		original := report.GetSurfaces()[1]
		duplicate := proto.Clone(original).(*stipulatorv1.BindingSurface)
		duplicate.SetRequirementIds([]string{"REQ-fixture-alpha"})
		duplicate.SetBindings([]*stipulatorv1.SurfaceBinding{proto.Clone(original.GetBindings()[0]).(*stipulatorv1.SurfaceBinding)})
		duplicate.SetId(identifier(
			implementation{backend: duplicate.GetBackend(), symbol: duplicate.GetSymbol()},
			duplicate.GetRequirementIds(),
			[]associated{{role: duplicate.GetBindings()[0].GetRole(), backend: duplicate.GetBindings()[0].GetBackend(), symbol: duplicate.GetBindings()[0].GetSymbol()}},
		))
		surfaces := report.GetSurfaces()
		report.SetSurfaces(append(surfaces[:2], append([]*stipulatorv1.BindingSurface{duplicate}, surfaces[2:]...)...))
	})
	mutate("invalid/noncanonical-requirements.json", func(report *stipulatorv1.BindingSurfaceReport) {
		slices.Reverse(report.GetSurfaces()[0].GetRequirementIds())
	})
	mutate("invalid/noncanonical-bindings.json", func(report *stipulatorv1.BindingSurfaceReport) {
		slices.Reverse(report.GetSurfaces()[0].GetBindings())
	})
	mutate("invalid/duplicate-requirement.json", func(report *stipulatorv1.BindingSurfaceReport) {
		surface := report.GetSurfaces()[0]
		surface.SetRequirementIds(append(surface.GetRequirementIds(), surface.GetRequirementIds()[1]))
	})
	mutate("invalid/duplicate-binding.json", func(report *stipulatorv1.BindingSurfaceReport) {
		surface := report.GetSurfaces()[0]
		bindings := surface.GetBindings()
		duplicated := []*stipulatorv1.SurfaceBinding{bindings[0], proto.Clone(bindings[0]).(*stipulatorv1.SurfaceBinding)}
		surface.SetBindings(append(duplicated, bindings[1:]...))
	})
	mutate("invalid/unknown-role.json", func(report *stipulatorv1.BindingSurfaceReport) {
		report.GetSurfaces()[0].GetBindings()[0].SetRole(stipulatorv1.BindingRole(99))
	})
	for _, path := range sortedReportPaths(invalid) {
		report := invalid[path]
		assertReportFixture(t, path, report)
	}

	consumerInvalid := map[string]*stipulatorv1.BindingSurfaceReport{}
	mutateConsumer := func(path string, fn func(*stipulatorv1.BindingSurface)) {
		report := proto.Clone(oneSurface).(*stipulatorv1.BindingSurfaceReport)
		surface := report.GetSurfaces()[0]
		fn(surface)
		surface.SetId(identifier(
			implementation{backend: surface.GetBackend(), symbol: surface.GetSymbol()},
			surface.GetRequirementIds(), associatedFromWire(surface.GetBindings()),
		))
		consumerInvalid[path] = report
	}
	mutateConsumer("gomutant-invalid/invalid-requirement.json", func(surface *stipulatorv1.BindingSurface) {
		surface.SetRequirementIds([]string{"invalid"})
	})
	mutateConsumer("gomutant-invalid/empty-implementation-symbol.json", func(surface *stipulatorv1.BindingSurface) {
		surface.SetSymbol("")
	})
	mutateConsumer("gomutant-invalid/empty-binding-symbol.json", func(surface *stipulatorv1.BindingSurface) {
		surface.GetBindings()[0].SetSymbol("")
	})
	for _, path := range sortedReportPaths(consumerInvalid) {
		assertReportFixture(t, path, consumerInvalid[path])
	}
}

func sortedReportPaths(reports map[string]*stipulatorv1.BindingSurfaceReport) []string {
	paths := make([]string, 0, len(reports))
	for path := range reports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func associatedFromWire(bindings []*stipulatorv1.SurfaceBinding) []associated {
	out := make([]associated, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, associated{role: binding.GetRole(), backend: binding.GetBackend(), symbol: binding.GetSymbol()})
	}
	return out
}

func TestMalformedContractFixtures(t *testing.T) {
	tests := []struct {
		path      string
		marker    string
		jsonValid bool
		wantErr   string
	}{
		{"invalid/unknown-field.json", `"unknown":true`, true, "unknown field"},
		{"invalid/duplicate-field.json", `"format":"stipulator.binding-surfaces/v1","format"`, true, "duplicate field"},
		{"invalid/trailing-json.json", `} {}`, false, "syntax error"},
		{"invalid/invalid-surrogate.json", `\ud800`, true, "syntax error"},
	}
	for _, test := range tests {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "v1", test.path))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(test.marker)) {
				t.Fatalf("fixture lost marker %q: %s", test.marker, raw)
			}
			if json.Valid(raw) != test.jsonValid {
				t.Fatalf("JSON validity = %t, want %t: %s", json.Valid(raw), test.jsonValid, raw)
			}
			err = protojson.Unmarshal(raw, &stipulatorv1.BindingSurfaceReport{})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("malformed fixture error = %v: %s", err, raw)
			}
		})
	}
}

func TestNullFixturePinsStrictConsumerBoundary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "v1", "gomutant-invalid", "null-surfaces.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"surfaces":null`)) {
		t.Fatalf("null fixture lost its isolated violation: %s", raw)
	}
	if err := protojson.Unmarshal(raw, &stipulatorv1.BindingSurfaceReport{}); err != nil {
		t.Fatalf("null fixture should be valid permissive ProtoJSON for the strict consumer to reject: %v", err)
	}
}

func fixtureFullReport(t *testing.T) *stipulatorv1.BindingSurfaceReport {
	t.Helper()
	report, err := Derive(testSpec("REQ-fixture-alpha", "REQ-fixture-beta", "REQ-fixture-empty", "REQ-fixture-shared"), testStore(
		binding("REQ-fixture-empty", "go", "example.com/p.Empty", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-beta", "go", "example.com/p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-alpha", "go", "example.com/p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-shared", "go", "example.com/p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-shared", "go", "example.com/p.G", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-alpha", "go", "example.com/p.TestA", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-fixture-beta", "go", "example.com/p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-fixture-alpha", "go", "example.com/p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_PROVES),
		binding("REQ-fixture-shared", "go", "example.com/p.TestSharedRequirement", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func fixtureEmptyReport(t *testing.T) *stipulatorv1.BindingSurfaceReport {
	t.Helper()
	report, err := Derive(testSpec("REQ-fixture-empty"), testStore(
		binding("REQ-fixture-empty", "go", "example.com/p.TestOnly", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func fixtureInvalidBaseReport(t *testing.T) *stipulatorv1.BindingSurfaceReport {
	t.Helper()
	report, err := Derive(testSpec("REQ-fixture-alpha", "REQ-fixture-beta"), testStore(
		binding("REQ-fixture-beta", "go", "example.com/p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-alpha", "go", "example.com/p.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-alpha", "go", "example.com/p.TestA", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-fixture-beta", "go", "example.com/p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-fixture-alpha", "go", "example.com/p.TestShared", stipulatorv1.BindingRole_BINDING_ROLE_PROVES),
	))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func fixtureSurfaceOrderReport(t *testing.T) *stipulatorv1.BindingSurfaceReport {
	t.Helper()
	report := fixtureInvalidBaseReport(t)
	empty, err := Derive(testSpec("REQ-fixture-empty"), testStore(
		binding("REQ-fixture-empty", "go", "example.com/p.Empty", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	report.SetSurfaces(append(empty.GetSurfaces(), report.GetSurfaces()...))
	return report
}

func fixtureMixedBackendReport(t *testing.T) *stipulatorv1.BindingSurfaceReport {
	t.Helper()
	report, err := Derive(testSpec("REQ-fixture-mixed-associated", "REQ-fixture-mixed-implementation"), testStore(
		binding("REQ-fixture-mixed-associated", "go", "example.com/p.Mixed", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-mixed-associated", "proto", "example.com/p.Check", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
		binding("REQ-fixture-mixed-implementation", "proto", "example.com/p.Proto", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-mixed-implementation", "go", "example.com/p.TestProto", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func fixtureUnicodeReport(t *testing.T) *stipulatorv1.BindingSurfaceReport {
	t.Helper()
	report, err := Derive(testSpec("REQ-fixture-unicode"), testStore(
		binding("REQ-fixture-unicode", "go", "example.com/café.F", stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS),
		binding("REQ-fixture-unicode", "go", "example.com/café.Test", stipulatorv1.BindingRole_BINDING_ROLE_TESTS),
	))
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func assertReportFixture(t *testing.T, path string, want *stipulatorv1.BindingSurfaceReport) {
	t.Helper()
	fullPath := filepath.Join("testdata", "v1", path)
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	got := &stipulatorv1.BindingSurfaceReport{}
	if err := protojson.Unmarshal(raw, got); err != nil {
		t.Fatalf("fixture %s is not ProtoJSON: %v", fullPath, err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("fixture %s differs:\ngot  %v\nwant %v", fullPath, got, want)
	}
	wantRaw, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var gotJSON, wantJSON any
	if err := json.Unmarshal(raw, &gotJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantRaw, &wantJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("fixture %s has noncanonical ProtoJSON representation:\ngot  %s\nwant %s", fullPath, raw, wantRaw)
	}
}
