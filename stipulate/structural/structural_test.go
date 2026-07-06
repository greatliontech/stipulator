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

func (r *recorder) Helper()                   {}
func (r *recorder) Errorf(f string, a ...any) { r.errors = append(r.errors, fmt.Sprintf(f, a...)) }
func (r *recorder) Fatalf(f string, a ...any) { r.fatals = append(r.fatals, fmt.Sprintf(f, a...)) }

const mod = "github.com/greatliontech/stipulator"

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
		// harden legitimately imports the go backend: a known edge to
		// detect.
		NoImport(r, mod+"/internal/harden", mod+"/internal/backends/...")
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

func TestImplements(t *testing.T) {
	r := &recorder{}
	Implements(r, (*strings.Reader)(nil), (*io.Reader)(nil))
	if len(r.errors)+len(r.fatals) != 0 {
		t.Fatalf("satisfied interface failed: %v %v", r.errors, r.fatals)
	}
	Implements(r, (*strings.Reader)(nil), (*io.Closer)(nil))
	if len(r.errors) == 0 || !strings.Contains(r.errors[0], "missing method Close") {
		t.Fatalf("missing method not named: %v", r.errors)
	}
	Implements(r, (*strings.Reader)(nil), "not an interface pointer")
	if len(r.fatals) == 0 {
		t.Fatal("malformed iface argument accepted")
	}
}
