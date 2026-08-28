package golang

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// A method whose receiver wears parentheses — the legal ((*T)) form —
// resolves through lookup exactly like its bare twin: the resolution
// is method-set-based, and parentheses are pure syntax the type
// checker never records. The AST-side reducers elsewhere (gofresh's
// recvTypeName and gomutant's) unwrap the same forms by hand; this
// backend is immune by construction, pinned here. The OTHER
// receiver-bearing path — the test-variant compartment ledger's
// declaration receiver — is immune for a different reason: it
// carries raw source text end to end (never reduced to a symbol,
// never matched against T.M), so no grammar can misread it.
//
// Promotion is part of the same pin, on the admitting side: T.M for
// a method promoted from an embedded U resolves — to the DECLARED
// object, exactly as Go's selector semantics denote — because the
// corpus binds that behavior (a wrapper's promoted-from-generated
// method must classify as generated: REQ-go-static-binding,
// REQ-go-generated-detect), and because the shape pin is taken over
// the declared object: if T later declares its own M, the resolved
// shape changes and the pin stales into re-consent, so the retarget
// is never silent. The declarer-only convention is the AST
// reducers' alone; gofresh's types-side subject walk admits
// promotion exactly as this lookup does, so subject naming and
// binding naming agree.
func TestLookupResolvesParenthesizedReceiverMethods(t *testing.T) {
	const src = `package p

type T struct{ U }

type U struct{}

type J struct{ I }

type I interface{ FromIface() }

func ((*T)) Paren()   {}
func (((T))) Nested() {}
func (*T) Bare()      {}
func (U) Promoted()   {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check("example.com/p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	declared := lookup(pkg, []string{"U", "Promoted"})
	if declared == nil {
		t.Fatal("lookup(U.Promoted) = nil — the declarer's own name must resolve")
	}
	promoted := lookup(pkg, []string{"T", "Promoted"})
	if promoted == nil {
		t.Fatal("lookup(T.Promoted) = nil — the promoted method must resolve under the embedder's name")
	}
	if promoted != declared {
		t.Errorf("lookup(T.Promoted) = %v, want the declared method %v", promoted, declared)
	}
	// The admission is exactly as wide as the method set: an embedded
	// INTERFACE's method resolves too — to the bodiless interface
	// method, which no FuncDecl matches, so it degrades to
	// not-a-runnable-witness downstream rather than erroring.
	fromIface := lookup(pkg, []string{"J", "FromIface"})
	if fromIface == nil {
		t.Fatal("lookup(J.FromIface) = nil — the embedded interface's method must resolve")
	}
	if fromIface != lookup(pkg, []string{"I", "FromIface"}) {
		t.Errorf("lookup(J.FromIface) = %v, want the interface's declared method", fromIface)
	}
	for _, method := range []string{"Paren", "Nested", "Bare"} {
		obj := lookup(pkg, []string{"T", method})
		if obj == nil {
			t.Errorf("lookup(T.%s) = nil — the parenthesized receiver defeated method resolution", method)
			continue
		}
		if obj.Name() != method {
			t.Errorf("lookup(T.%s) resolved %q", method, obj.Name())
		}
	}
}
