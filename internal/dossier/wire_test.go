package dossier

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestBindingSurfaceWireContract(t *testing.T) {
	tests := []struct {
		message protoreflect.MessageDescriptor
		fields  []fieldContract
	}{
		{(&stipulatorv1.SurfaceBinding{}).ProtoReflect().Descriptor(), []fieldContract{
			{"backend", 1, protoreflect.StringKind, false, ""},
			{"role", 2, protoreflect.EnumKind, false, "stipulator.v1.BindingRole"},
			{"symbol", 3, protoreflect.StringKind, false, ""},
		}},
		{(&stipulatorv1.BindingSurface{}).ProtoReflect().Descriptor(), []fieldContract{
			{"id", 1, protoreflect.StringKind, false, ""},
			{"backend", 2, protoreflect.StringKind, false, ""},
			{"symbol", 3, protoreflect.StringKind, false, ""},
			{"requirement_ids", 4, protoreflect.StringKind, true, ""},
			{"bindings", 5, protoreflect.MessageKind, true, "stipulator.v1.SurfaceBinding"},
		}},
		{(&stipulatorv1.BindingSurfaceReport{}).ProtoReflect().Descriptor(), []fieldContract{
			{"surfaces", 1, protoreflect.MessageKind, true, "stipulator.v1.BindingSurface"},
			{"format", 2, protoreflect.StringKind, false, ""},
		}},
	}
	for _, test := range tests {
		if test.message.Fields().Len() != len(test.fields) {
			t.Fatalf("%s has %d fields, want %d", test.message.Name(), test.message.Fields().Len(), len(test.fields))
		}
		for _, want := range test.fields {
			field := test.message.Fields().ByName(protoreflect.Name(want.name))
			if field == nil || field.Number() != want.number || field.Kind() != want.kind || field.IsList() != want.list {
				t.Fatalf("%s.%s = %v, want number=%d kind=%s list=%t", test.message.Name(), want.name, field, want.number, want.kind, want.list)
			}
			var typeName protoreflect.FullName
			switch field.Kind() {
			case protoreflect.EnumKind:
				typeName = field.Enum().FullName()
			case protoreflect.MessageKind:
				typeName = field.Message().FullName()
			}
			if typeName != want.typeName {
				t.Fatalf("%s.%s type = %s, want %s", test.message.Name(), want.name, typeName, want.typeName)
			}
		}
	}

	dossier := (&stipulatorv1.Dossier{}).ProtoReflect().Descriptor()
	if !dossier.ReservedRanges().Has(6) || !dossier.ReservedNames().Has("hardening") {
		t.Fatal("Dossier does not reserve retired hardening field 6")
	}
}

type fieldContract struct {
	name     string
	number   protoreflect.FieldNumber
	kind     protoreflect.Kind
	list     bool
	typeName protoreflect.FullName
}
