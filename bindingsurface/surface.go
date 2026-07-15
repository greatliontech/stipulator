// Package bindingsurface owns Stipulator's binding-surface wire contract for
// Go producers and consumers.
package bindingsurface

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	surfacev1 "github.com/greatliontech/stipulator/bindingsurface/gen/stipulator/surface/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Format identifies the binding-surface ProtoJSON contract.
const Format = "stipulator.binding-surfaces/v1"

const domain = "stipulator-binding-surface-v1"

// Public aliases keep callers on the module API while preserving generated
// protobuf reflection and ProtoJSON behavior.
type (
	Report      = surfacev1.BindingSurfaceReport
	Surface     = surfacev1.BindingSurface
	Binding     = surfacev1.SurfaceBinding
	BindingRole = surfacev1.BindingRole
)

const (
	BindingRoleUnspecified = surfacev1.BindingRole_BINDING_ROLE_UNSPECIFIED
	BindingRoleTests       = surfacev1.BindingRole_BINDING_ROLE_TESTS
	BindingRoleProves      = surfacev1.BindingRole_BINDING_ROLE_PROVES
)

var jsonOutput = protojson.MarshalOptions{EmitUnpopulated: true}

// ParseJSON decodes the strict canonical ProtoJSON form and validates every
// relationship and identifier before returning it.
func ParseJSON(data []byte) (*Report, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("binding surfaces: invalid UTF-8")
	}
	report := &Report{}
	if err := protojson.Unmarshal(data, report); err != nil {
		return nil, fmt.Errorf("binding surfaces: parse ProtoJSON: %w", err)
	}
	if err := Validate(report); err != nil {
		return nil, err
	}
	canonical, err := jsonOutput.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("binding surfaces: render canonical ProtoJSON: %w", err)
	}
	got, err := jsonValue(data)
	if err != nil {
		return nil, fmt.Errorf("binding surfaces: parse JSON value: %w", err)
	}
	want, err := jsonValue(canonical)
	if err != nil {
		return nil, fmt.Errorf("binding surfaces: parse canonical JSON value: %w", err)
	}
	if !reflect.DeepEqual(got, want) {
		return nil, fmt.Errorf("binding surfaces: document is not canonical ProtoJSON")
	}
	return report, nil
}

// MarshalJSON validates and renders the canonical ProtoJSON representation.
// JSON whitespace and object-member order are not stable or contractual.
func MarshalJSON(report *Report) ([]byte, error) {
	if err := Validate(report); err != nil {
		return nil, err
	}
	data, err := jsonOutput.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("binding surfaces: render ProtoJSON: %w", err)
	}
	return data, nil
}

// Validate checks format, field domains, canonical ordering, uniqueness, and
// every relationship identifier.
func Validate(report *Report) error {
	if report == nil {
		return fmt.Errorf("binding surfaces: nil report")
	}
	if !report.HasFormat() || report.GetFormat() != Format {
		return fmt.Errorf("binding surfaces: format %q not understood (want %q)", report.GetFormat(), Format)
	}
	for i, surface := range report.GetSurfaces() {
		if surface == nil {
			return fmt.Errorf("binding surfaces: surface %d is null", i)
		}
		if err := validateSurface(surface, true); err != nil {
			return fmt.Errorf("binding surfaces: surface %d: %w", i, err)
		}
		if i > 0 {
			previous := report.GetSurfaces()[i-1]
			switch compareSurface(previous, surface) {
			case 0:
				return fmt.Errorf("binding surfaces: duplicate surface %s %s", surface.GetBackend(), surface.GetSymbol())
			case 1:
				return fmt.Errorf("binding surfaces: surfaces are not canonically ordered at %s %s", surface.GetBackend(), surface.GetSymbol())
			}
		}
	}
	return nil
}

// Identifier validates one ID-less surface relationship and returns its
// canonical lowercase SHA-256 identifier.
func Identifier(surface *Surface) (string, error) {
	if surface == nil {
		return "", fmt.Errorf("binding surfaces: nil surface")
	}
	if err := validateSurface(surface, false); err != nil {
		return "", fmt.Errorf("binding surfaces: %w", err)
	}
	var canonical strings.Builder
	writeString(&canonical, domain)
	writeString(&canonical, surface.GetBackend())
	writeString(&canonical, surface.GetSymbol())
	writeCount(&canonical, len(surface.GetRequirementIds()))
	for _, requirement := range surface.GetRequirementIds() {
		writeString(&canonical, requirement)
	}
	writeCount(&canonical, len(surface.GetBindings()))
	for _, binding := range surface.GetBindings() {
		writeString(&canonical, roleToken(binding.GetRole()))
		writeString(&canonical, binding.GetBackend())
		writeString(&canonical, binding.GetSymbol())
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:]), nil
}

func validateSurface(surface *Surface, checkID bool) error {
	if !surface.HasBackend() || surface.GetBackend() == "" || !utf8.ValidString(surface.GetBackend()) {
		return fmt.Errorf("backend is missing or invalid")
	}
	if !surface.HasSymbol() || surface.GetSymbol() == "" || !utf8.ValidString(surface.GetSymbol()) {
		return fmt.Errorf("symbol is missing or invalid")
	}
	if len(surface.GetRequirementIds()) == 0 {
		return fmt.Errorf("requirementIds is empty")
	}
	for i, requirement := range surface.GetRequirementIds() {
		if !validRequirement(requirement) {
			return fmt.Errorf("requirementIds element %d is invalid", i)
		}
		if i > 0 && surface.GetRequirementIds()[i-1] >= requirement {
			return fmt.Errorf("requirementIds is duplicated or not canonically ordered at %q", requirement)
		}
	}
	for i, binding := range surface.GetBindings() {
		if binding == nil {
			return fmt.Errorf("binding %d is null", i)
		}
		if !binding.HasBackend() || binding.GetBackend() == "" || !utf8.ValidString(binding.GetBackend()) {
			return fmt.Errorf("binding %d backend is missing or invalid", i)
		}
		if !binding.HasSymbol() || binding.GetSymbol() == "" || !utf8.ValidString(binding.GetSymbol()) {
			return fmt.Errorf("binding %d symbol is missing or invalid", i)
		}
		if !binding.HasRole() || (binding.GetRole() != BindingRoleTests && binding.GetRole() != BindingRoleProves) {
			return fmt.Errorf("binding %d role is not tests or proves", i)
		}
		if i > 0 && compareBinding(surface.GetBindings()[i-1], binding) >= 0 {
			return fmt.Errorf("bindings are duplicated or not canonically ordered at %s %s %s", binding.GetRole(), binding.GetBackend(), binding.GetSymbol())
		}
	}
	if !checkID {
		return nil
	}
	if !surface.HasId() || !lowerHex64(surface.GetId()) {
		return fmt.Errorf("id is not 64 lowercase hexadecimal characters")
	}
	want, err := Identifier(surface)
	if err != nil {
		return err
	}
	if surface.GetId() != want {
		return fmt.Errorf("id %s does not match relationship (want %s)", surface.GetId(), want)
	}
	return nil
}

func jsonValue(data []byte) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, err
	}
	return value, nil
}

func compareSurface(a, b *Surface) int {
	if result := strings.Compare(a.GetBackend(), b.GetBackend()); result != 0 {
		return result
	}
	return strings.Compare(a.GetSymbol(), b.GetSymbol())
}

func compareBinding(a, b *Binding) int {
	if roleRank(a.GetRole()) != roleRank(b.GetRole()) {
		return roleRank(a.GetRole()) - roleRank(b.GetRole())
	}
	if result := strings.Compare(a.GetBackend(), b.GetBackend()); result != 0 {
		return result
	}
	return strings.Compare(a.GetSymbol(), b.GetSymbol())
}

func roleRank(role BindingRole) int {
	if role == BindingRoleTests {
		return 0
	}
	return 1
}

func roleToken(role BindingRole) string {
	if role == BindingRoleTests {
		return "tests"
	}
	return "proves"
}

func writeString(out *strings.Builder, value string) {
	writeCount(out, len(value))
	out.WriteString(value)
}

func writeCount(out *strings.Builder, value int) {
	out.WriteString(strconv.Itoa(value))
	out.WriteByte(':')
}

func lowerHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range []byte(value) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validRequirement(value string) bool {
	if !strings.HasPrefix(value, "REQ-") {
		return false
	}
	for _, part := range strings.Split(value[4:], "-") {
		if part == "" {
			return false
		}
		for _, c := range []byte(part) {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
