package policy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// recordHeader heads the machine-written policy record, mirroring the
// record-store convention.
const recordHeader = "# proto-file: proto/stipulator/v1/policy.proto\n" +
	"# proto-message: stipulator.v1.TestPolicy\n\n"

// Render's output is canonical — and therefore accepted back by Parse — only
// for policies that pass Validate; every writer validates (directly or through
// Dispatch) before rendering. Render renders a policy record deterministically: the standard header,
// then every populated field in field-number order with fixed indentation
// and Go-quoted strings. prototext.Marshal deliberately destabilizes its
// whitespace, and the policy record is a committed, reviewed artifact
// whose bytes two hosts must agree on, so the record gets an owned
// renderer. The walk is generic over protobuf reflection: the core stays
// ignorant of backend payload contents even while serializing them.
func Render(p *stipulatorv1.TestPolicy) ([]byte, error) {
	var b strings.Builder
	b.WriteString(recordHeader)
	if err := renderFields(&b, p.ProtoReflect(), ""); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// renderFields writes m's populated fields in field-number order.
func renderFields(b *strings.Builder, m protoreflect.Message, indent string) error {
	var fds []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fds = append(fds, fd)
		return true
	})
	sort.Slice(fds, func(i, j int) bool { return fds[i].Number() < fds[j].Number() })
	for _, fd := range fds {
		v := m.Get(fd)
		switch {
		case fd.IsMap():
			// No policy message carries a map; refuse loudly rather than
			// invent an ordering the wire contract never specified.
			return fmt.Errorf("rendering %s: map fields are not part of the policy wire surface", fd.FullName())
		case fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				if err := renderField(b, fd, list.Get(i), indent); err != nil {
					return err
				}
			}
		default:
			if err := renderField(b, fd, v, indent); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderField writes one field occurrence: a nested block for messages, a
// single "name: value" line for scalars.
func renderField(b *strings.Builder, fd protoreflect.FieldDescriptor, v protoreflect.Value, indent string) error {
	name := string(fd.Name())
	if fd.Kind() == protoreflect.MessageKind {
		b.WriteString(indent + name + " {\n")
		if err := renderFields(b, v.Message(), indent+"  "); err != nil {
			return err
		}
		b.WriteString(indent + "}\n")
		return nil
	}
	s, err := renderScalar(fd, v)
	if err != nil {
		return err
	}
	b.WriteString(indent + name + ": " + s + "\n")
	return nil
}

// renderScalar formats one scalar value deterministically.
func renderScalar(fd protoreflect.FieldDescriptor, v protoreflect.Value) (string, error) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return strconv.Quote(v.String()), nil
	case protoreflect.BoolKind:
		return strconv.FormatBool(v.Bool()), nil
	case protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return strconv.FormatInt(v.Int(), 10), nil
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		return strconv.FormatUint(v.Uint(), 10), nil
	case protoreflect.EnumKind:
		if ev := fd.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return string(ev.Name()), nil
		}
		return strconv.FormatInt(int64(v.Enum()), 10), nil
	default:
		// Floats, bytes, and groups have no place in the policy record;
		// a future field of such a kind must extend the renderer (and the
		// canonical-form contract) deliberately.
		return "", fmt.Errorf("rendering %s: unsupported kind %v in a policy record", fd.FullName(), fd.Kind())
	}
}
