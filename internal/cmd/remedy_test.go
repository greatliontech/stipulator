package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/stipulator/internal/remedy"
	"github.com/greatliontech/stipulator/stipulate"
)

// Every composed remedy parses against the real command tree: its verb
// path resolves to a leaf command and its flags parse there, so a
// renamed verb or flag can never leave a rendered finding naming a
// spelling the tool no longer accepts (REQ-change-remediation).
//
//gofresh:pure
func TestEveryRemedyParsesAgainstTheCommandTree(t *testing.T) {
	stipulate.Covers(t, "REQ-change-remediation")
	samples := remedy.Samples()
	if len(samples) < 10 {
		t.Fatalf("the remedy corpus is %d samples; the composers are unlisted", len(samples))
	}
	// Every exported composer of the package is in the corpus: the
	// source is parsed, so a composer added without registering is
	// refused here rather than served unparsed.
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "../remedy", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, name := range unregisteredComposers(f, remedy.Registered()) {
				t.Errorf("composer %s is not registered, so nothing parses it (every exported function of the remedy package is a composer)", name)
			}
		}
	}
	for _, sample := range samples {
		words := strings.Fields(sample)
		root := newRootCmd()
		if len(words) == 0 || words[0] != root.Name() {
			t.Fatalf("%q does not begin with the root command's name %q", sample, root.Name())
		}
		cmd, rest, err := root.Find(words[1:])
		if err != nil || cmd == root || cmd.HasSubCommands() {
			t.Fatalf("%q resolves to no leaf command: %v (%v)", sample, cmd, err)
		}
		if err := cmd.ParseFlags(rest); err != nil {
			t.Fatalf("%q: its flags do not parse on %s: %v", sample, cmd.CommandPath(), err)
		}
		// Every remaining word is a registered flag or the value the
		// preceding non-boolean flag takes: a bare word is a verb the
		// tree did not resolve — a misspelled path — never an argument.
		for i := 0; i < len(rest); i++ {
			w := rest[i]
			if !strings.HasPrefix(w, "--") {
				t.Fatalf("%q leaves the bare word %q unresolved on %s", sample, w, cmd.CommandPath())
			}
			f := cmd.Flags().Lookup(strings.TrimPrefix(w, "--"))
			if f == nil {
				t.Fatalf("%q names a flag %s the command does not register", sample, w)
			}
			if f.Value.Type() != "bool" {
				i++
			}
		}
	}
}

// The registry check names exactly the exported functions the
// registry lacks: the accessors are exempt, unexported helpers and
// methods are not composers, and a registered name is an exact match,
// never a prefix of one.
//
//gofresh:pure
func TestUnregisteredComposersAreNamedExactly(t *testing.T) {
	src := `package remedy
func Pin() string { return "" }
func Attest() string { return "" }
func AttestRequirement() string { return "" }
func Samples() []string { return nil }
func Registered() []string { return nil }
func helper() string { return "" }
func (x kind) Method() string { return "" }
`
	f, err := parser.ParseFile(token.NewFileSet(), "remedy.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := unregisteredComposers(f, []string{"Pin", "AttestRequirement"})
	if want := []string{"Attest"}; !slices.Equal(got, want) {
		t.Fatalf("unregistered = %v, want %v", got, want)
	}
	if got := unregisteredComposers(f, []string{"Pin", "AttestRequirement", "Attest"}); len(got) != 0 {
		t.Fatalf("unregistered = %v, want none", got)
	}
}

// unregisteredComposers lists the exported functions of a remedy source
// file whose names the registry lacks; the two accessors are exempt.
func unregisteredComposers(f *ast.File, registered []string) []string {
	var out []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Name.Name == "Samples" || fn.Name.Name == "Registered" {
			continue
		}
		if !slices.Contains(registered, fn.Name.Name) {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}
