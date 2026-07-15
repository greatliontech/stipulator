package bindingsurface

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractFixtures(t *testing.T) {
	valid := []string{
		"empty.json",
		"full.json",
		"mixed-backend.json",
		"unicode.json",
	}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, filepath.Join("valid", name))
			report, err := ParseJSON(data)
			if err != nil {
				t.Fatalf("ParseJSON: %v", err)
			}
			rendered, err := MarshalJSON(report)
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if _, err := ParseJSON(rendered); err != nil {
				t.Fatalf("ParseJSON(MarshalJSON(report)): %v", err)
			}
		})
	}

	invalid := map[string]string{
		"invalid/bad-id.json":                               "does not match relationship",
		"invalid/duplicate-binding.json":                    "bindings are duplicated",
		"invalid/duplicate-field.json":                      "duplicate field",
		"invalid/duplicate-requirement.json":                "requirementIds is duplicated",
		"invalid/duplicate-surface-key.json":                "duplicate surface",
		"invalid/duplicate-surface.json":                    "duplicate surface",
		"invalid/invalid-surrogate.json":                    "invalid escape code",
		"invalid/mismatched-id.json":                        "does not match relationship",
		"invalid/noncanonical-bindings.json":                "not canonically ordered",
		"invalid/noncanonical-requirements.json":            "not canonically ordered",
		"invalid/noncanonical-surfaces.json":                "surfaces are not canonically ordered",
		"invalid/trailing-json.json":                        "unexpected token",
		"invalid/unknown-field.json":                        "unknown field",
		"invalid/unknown-format.json":                       "not understood",
		"invalid/unknown-role.json":                         "role is not tests or proves",
		"gomutant-invalid/empty-binding-symbol.json":        "binding 0 symbol is missing or invalid",
		"gomutant-invalid/empty-implementation-symbol.json": "symbol is missing or invalid",
		"gomutant-invalid/invalid-requirement.json":         "requirementIds element 0 is invalid",
		"gomutant-invalid/null-surfaces.json":               "document is not canonical ProtoJSON",
	}
	for path, wantError := range invalid {
		t.Run(path, func(t *testing.T) {
			data := readFixture(t, path)
			_, err := ParseJSON(data)
			if err == nil {
				t.Fatal("ParseJSON accepted invalid fixture")
			}
			if !strings.Contains(err.Error(), wantError) {
				t.Fatalf("ParseJSON error = %q, want substring %q", err, wantError)
			}
		})
	}
}

func TestParseJSONRequiresExactRepresentation(t *testing.T) {
	empty := `{"surfaces":[],"format":"stipulator.binding-surfaces/v1"}`
	valid := []string{
		empty,
		" \n { \"format\" : \"stipulator.binding-surfaces/v1\", \"surfaces\" : [ ] } \n ",
	}
	for _, document := range valid {
		if _, err := ParseJSON([]byte(document)); err != nil {
			t.Errorf("ParseJSON(%q): %v", document, err)
		}
	}

	mixed := string(readFixture(t, filepath.Join("valid", "mixed-backend.json")))
	invalid := map[string]string{
		"omitted field":      `{"format":"stipulator.binding-surfaces/v1"}`,
		"null field":         `{"surfaces":null,"format":"stipulator.binding-surfaces/v1"}`,
		"duplicate field":    `{"surfaces":[],"surfaces":[],"format":"stipulator.binding-surfaces/v1"}`,
		"alternate spelling": strings.Replace(mixed, `"requirementIds"`, `"requirement_ids"`, 1),
		"numeric enum":       strings.Replace(mixed, `"BINDING_ROLE_TESTS"`, `1`, 1),
		"trailing value":     empty + `{}`,
	}
	for name, document := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseJSON([]byte(document)); err == nil {
				t.Fatal("ParseJSON accepted non-contract representation")
			}
		})
	}
}

func TestMarshalJSONEmitsRequiredEmptyCollections(t *testing.T) {
	report := &Report{}
	report.SetFormat(Format)
	report.SetSurfaces([]*Surface{})
	data, err := MarshalJSON(report)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !bytes.Contains(data, []byte(`"surfaces":[]`)) {
		t.Fatalf("MarshalJSON omitted empty surfaces: %s", data)
	}
}

func TestIdentifierAnchor(t *testing.T) {
	surface := &Surface{}
	surface.SetBackend("go")
	surface.SetSymbol("p.F")
	surface.SetRequirementIds([]string{"REQ-a"})

	tests := &Binding{}
	tests.SetRole(BindingRoleTests)
	tests.SetBackend("go")
	tests.SetSymbol("p.TestF")
	surface.SetBindings([]*Binding{tests})

	got, err := Identifier(surface)
	if err != nil {
		t.Fatalf("Identifier: %v", err)
	}
	const want = "ed0330a6f616587e4597de19c3b9681a255f452e5c1eeee96860aab92f4997f9"
	if got != want {
		t.Fatalf("Identifier = %q, want %q", got, want)
	}
}

func TestIdentifierRejectsNoncanonicalRelationship(t *testing.T) {
	surface := &Surface{}
	surface.SetBackend("go")
	surface.SetSymbol("p.F")
	surface.SetRequirementIds([]string{"REQ-a"})

	proves := &Binding{}
	proves.SetRole(BindingRoleProves)
	proves.SetBackend("go")
	proves.SetSymbol("p.ProofF")
	tests := &Binding{}
	tests.SetRole(BindingRoleTests)
	tests.SetBackend("go")
	tests.SetSymbol("p.TestF")
	surface.SetBindings([]*Binding{proves, tests})

	if _, err := Identifier(surface); err == nil {
		t.Fatal("Identifier accepted noncanonical binding order")
	}
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "v1", path))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return data
}
