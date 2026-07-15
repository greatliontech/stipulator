package structural

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// recorder captures failures without failing, so the assertions' negative
// arms are testable.
type recorder struct {
	testing.TB
	errors []string
	fatals []string
}

type PublicData struct {
	Name  string
	Count int
}

type PublicHiddenData struct {
	Name   string
	hidden int
}

type PublicEmbeddedData struct {
	PublicData
}

type PublicTaggedData struct {
	Name string `json:"name"`
}

type PublicMethodData struct {
	Name string
}

func (PublicMethodData) String() string { return "" }

type PublicPointerMethodData struct {
	Name string
}

func (*PublicPointerMethodData) Reset() {}

type PublicPrivateMethodData struct {
	Name string
}

func (PublicPrivateMethodData) normalize() {}

type privateData struct {
	Name string
}

func (r *recorder) Helper()                   {}
func (r *recorder) Errorf(f string, a ...any) { r.errors = append(r.errors, fmt.Sprintf(f, a...)) }
func (r *recorder) Fatalf(f string, a ...any) { r.fatals = append(r.fatals, fmt.Sprintf(f, a...)) }

const mod = "github.com/greatliontech/stipulator"

// Deliberately not //gofresh:pure: the verdict depends on the import
// graph of module packages outside this test binary's closure, read
// through a go list child the testlog cannot observe. A cached pass
// could serve while an audited package drifts; the witness re-runs
// every gate.
func TestNoImport(t *testing.T) {
	t.Run("clean constraint passes", func(t *testing.T) {
		r := &recorder{}
		NoImport(r, mod+"/internal/canon", mod+"/internal/backends/...")
		if len(r.errors)+len(r.fatals) != 0 {
			t.Fatalf("clean constraint failed: %v %v", r.errors, r.fatals)
		}
	})
	t.Run("violation names the chain", func(t *testing.T) {
		r := &recorder{}
		// The CLI legitimately imports the Go backend: a known edge to detect.
		NoImport(r, mod+"/internal/cmd", mod+"/internal/backends/...")
		if len(r.errors) == 0 {
			t.Fatal("violation not reported")
		}
		if !strings.Contains(r.errors[0], "chain:") || !strings.Contains(r.errors[0], "internal/backends/golang") {
			t.Fatalf("chain missing: %s", r.errors[0])
		}
	})
	t.Run("vacuous pattern refused", func(t *testing.T) {
		r := &recorder{}
		NoImport(r, mod+"/internal/nosuchpkg", "os/exec")
		// A nonexistent pattern refuses loudly — as a load error or as the
		// explicit vacuity message, never as a silent pass.
		if len(r.fatals) == 0 {
			t.Fatalf("vacuous constraint accepted: %v", r.fatals)
		}
	})
	t.Run("exact match does not swallow subtrees", func(t *testing.T) {
		r := &recorder{}
		// canon imports golang.org/x/text/unicode/norm; forbidding the
		// exact path "golang.org/x/text" (no /...) must not match it.
		NoImport(r, mod+"/internal/canon", "golang.org/x/text")
		if len(r.errors) != 0 {
			t.Fatalf("exact match behaved as prefix: %v", r.errors)
		}
		NoImport(r, mod+"/internal/canon", "golang.org/x/text/...")
		if len(r.errors) == 0 {
			t.Fatal("subtree match missed a transitive import")
		}
	})
}

//gofresh:pure
func TestImplements(t *testing.T) {
	r := &recorder{}
	Implements[io.Reader](r, (*strings.Reader)(nil))
	if len(r.errors)+len(r.fatals) != 0 {
		t.Fatalf("satisfied interface failed: %v %v", r.errors, r.fatals)
	}
	Implements[io.Closer](r, (*strings.Reader)(nil))
	if len(r.errors) == 0 || !strings.Contains(r.errors[0], "missing method Close") {
		t.Fatalf("missing method not named: %v", r.errors)
	}
	// A non-interface type parameter would silently assert type identity
	// — a different claim — so it is refused.
	Implements[strings.Reader](r, (*strings.Reader)(nil))
	if len(r.fatals) == 0 {
		t.Fatal("non-interface type parameter accepted")
	}
	// An untyped nil carries no type to check.
	Implements[io.Reader](r, nil)
	if len(r.fatals) < 2 {
		t.Fatal("untyped nil accepted")
	}
}

//gofresh:pure
func TestExportedData(t *testing.T) {
	t.Run("exact exported shape passes", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicData](r, FieldOf[string]("Name"), FieldOf[int]("Count"))
		if len(r.errors)+len(r.fatals) != 0 {
			t.Fatalf("exact data shape failed: %v %v", r.errors, r.fatals)
		}
	})
	t.Run("wrong field name fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicData](r, FieldOf[string]("Wrong"), FieldOf[int]("Count"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "field 0") {
			t.Fatalf("wrong field name accepted: %v", r.errors)
		}
	})
	t.Run("wrong field type fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicData](r, FieldOf[int]("Name"), FieldOf[int]("Count"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "field 0") {
			t.Fatalf("wrong field type accepted: %v", r.errors)
		}
	})
	t.Run("hidden state fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicHiddenData](r, FieldOf[string]("Name"), FieldOf[int]("hidden"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "unexported") {
			t.Fatalf("hidden field accepted: %v", r.errors)
		}
	})
	t.Run("embedding fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicEmbeddedData](r, FieldOf[PublicData]("PublicData"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "embedded") {
			t.Fatalf("embedded field accepted: %v", r.errors)
		}
	})
	t.Run("pointer methods fail", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicPointerMethodData](r, FieldOf[string]("Name"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "has methods") {
			t.Fatalf("pointer method-bearing data accepted: %v", r.errors)
		}
	})
	t.Run("unexported helper passes", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicPrivateMethodData](r, FieldOf[string]("Name"))
		if len(r.errors)+len(r.fatals) != 0 {
			t.Fatalf("unexported helper changed public data shape: %v %v", r.errors, r.fatals)
		}
	})
	t.Run("anonymous struct fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[struct{ Name string }](r, FieldOf[string]("Name"))
		if len(r.fatals) == 0 || !strings.Contains(r.fatals[0], "not an exported named type") {
			t.Fatalf("anonymous struct accepted: %v", r.fatals)
		}
	})
	t.Run("unexported named type fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[privateData](r, FieldOf[string]("Name"))
		if len(r.fatals) == 0 || !strings.Contains(r.fatals[0], "not an exported named type") {
			t.Fatalf("unexported named type accepted: %v", r.fatals)
		}
	})
	t.Run("tag fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicTaggedData](r, FieldOf[string]("Name"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "has tag") {
			t.Fatalf("tagged field accepted: %v", r.errors)
		}
	})
	t.Run("methods fail", func(t *testing.T) {
		r := &recorder{}
		ExportedData[PublicMethodData](r, FieldOf[string]("Name"))
		if len(r.errors) == 0 || !strings.Contains(r.errors[0], "has methods") {
			t.Fatalf("method-bearing data accepted: %v", r.errors)
		}
	})
	t.Run("non-struct fails", func(t *testing.T) {
		r := &recorder{}
		ExportedData[int](r)
		if len(r.fatals) == 0 {
			t.Fatal("non-struct accepted")
		}
	})
}

//gofresh:pure
func TestFunctionSignature(t *testing.T) {
	fn := func(string) bool { return true }
	t.Run("exact signature passes", func(t *testing.T) {
		r := &recorder{}
		FunctionSignature[func(string) bool](r, fn)
		if len(r.errors)+len(r.fatals) != 0 {
			t.Fatalf("exact function signature failed: %v %v", r.errors, r.fatals)
		}
	})
	t.Run("mismatch fails", func(t *testing.T) {
		r := &recorder{}
		FunctionSignature[func(int) bool](r, fn)
		if len(r.errors) == 0 {
			t.Fatal("mismatched function signature accepted")
		}
	})
	t.Run("non-function signature fails", func(t *testing.T) {
		r := &recorder{}
		FunctionSignature[string](r, fn)
		if len(r.fatals) == 0 {
			t.Fatal("non-function signature type accepted")
		}
	})
	t.Run("non-function value fails", func(t *testing.T) {
		r := &recorder{}
		FunctionSignature[func(string) bool](r, 1)
		if len(r.fatals) == 0 {
			t.Fatal("non-function value accepted")
		}
	})
}
