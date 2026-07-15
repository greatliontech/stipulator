package harden

import (
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

const doc = "# T\n\n**REQ-h-strong** (behavior): It MUST add.\n\n**REQ-h-weak** (behavior): It MUST weaken.\n\n**REQ-h-untested** (behavior): It MUST float.\n\n**REQ-h-shared** (behavior): It MUST share.\n\n**REQ-h-typed** (behavior): It MUST type.\n"

func fixture(t *testing.T, extra map[string]string) (*stipulatorv1.Spec, *records.Store) {
	t.Helper()
	fsys := fstest.MapFS{
		".stipulator/manifest.textproto": {Data: []byte("include: \"specs/**/*.md\"\n")},
		"specs/a.md":                     {Data: []byte(doc)},
		".stipulator/bindings/h.textproto": {Data: []byte(`bindings {
  requirement_id: "REQ-h-strong"
  backend: "go"
  symbol: "example.com/fixture/lib.Add"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-strong"
  backend: "go"
  symbol: "example.com/fixture/lib.TestAdd"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-weak"
  backend: "go"
  symbol: "example.com/fixture/lib.Weak"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-weak"
  backend: "go"
  symbol: "example.com/fixture/lib.TestWeak"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-untested"
  backend: "go"
  symbol: "example.com/fixture/lib.F"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-shared"
  backend: "go"
  symbol: "example.com/fixture/lib.Weak"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-shared"
  backend: "go"
  symbol: "example.com/fixture/lib.TestAdd"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-shared"
  backend: "go"
  symbol: "example.com/fixture/plain.TestPlain"
  role: BINDING_ROLE_TESTS
}
bindings {
  requirement_id: "REQ-h-typed"
  backend: "go"
  symbol: "example.com/fixture/lib.W"
  role: BINDING_ROLE_IMPLEMENTS
}
bindings {
  requirement_id: "REQ-h-typed"
  backend: "go"
  symbol: "example.com/fixture/lib.TestAdd"
  role: BINDING_ROLE_TESTS
}
`)},
	}
	for p, c := range extra {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
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
