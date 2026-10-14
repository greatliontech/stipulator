package golang

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestDiscoveryMatchesGoAliasedHandles compares discovery and selective
// execution with the Go harness, including aliases declared in other files
// and packages. The independently executed inventory anchors the syntax rule.
func TestDiscoveryMatchesGoAliasedHandles(t *testing.T) {
	if testing.Short() {
		t.Skip("compares actual Go execution with discovered selective obligations")
	}
	stipulate.Covers(t, "REQ-go-policy-complete-obligation-identity", "REQ-policy-conservation")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":            "module example.com/aliases\n\ngo 1.27\n",
		"p/p.go":            "package p\n",
		"p/handles_test.go": "package p\nimport tt \"testing\"\ntype T = tt.T\ntype F = tt.F\n",
		"p/alias_test.go": `package p
func TestAlias(t *T) { t.Log("alias executed") }
func TestMain(*T) {}
func Test(*T) {}
type helper struct{}
func (helper) TestMethod(*T) { panic("method ran") }
func FuzzAlias(f *F) { f.Add("inline"); f.Fuzz(func(t *T, value string) {}) }
`,
		"p/qualified_test.go": `package p
import tt "testing"
import bridge "example.com/aliases/bridge"
func TestQualified(*tt.T) {}
func TestBridge(*bridge.T) {}
func FuzzBridge(f *bridge.F) { f.Add("bridge"); f.Fuzz(func(t *tt.T, value string) {}) }
`,
		"bridge/types.go":                     "package bridge\nimport \"testing\"\ntype T = testing.T\ntype F = testing.F\n",
		"dot/dot_test.go":                     "package dot\nimport . \"testing\"\nfunc TestDot(*T) {}\nfunc TestMain(m *M) { m.Run() }\n",
		"p/external_handles_test.go":          "package p_test\nimport \"testing\"\ntype T = testing.T\n",
		"p/external_test.go":                  "package p_test\nfunc TestExternalAlias(*T) {}\n",
		"p/testdata/fuzz/FuzzAlias/committed": "go test fuzz v1\nstring(\"committed\")\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./p", "./dot"})
	cfg.SetPlainWitness(true)
	inv := goInvocation("aliases", cfg)
	inv.SetTimeout(durationpb.New(2 * time.Minute))
	n, err := NormalizeInvocation(t.Context(), dir, inv)
	if err != nil {
		t.Fatal(err)
	}
	// The independent compiler/harness executes before the inventory assertion:
	// a merely test-shaped declaration is not evidence that Go accepts the type.
	cmd := exec.CommandContext(t.Context(), "go", "test", "-json", "-count=1", "./p", "./dot")
	cmd.Dir, cmd.Env = n.Dir, n.Env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("independent Go execution: %v\n%s\n%s", err, out, &stderr)
	}
	var actual []string
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var event struct{ Action, Package, Test string }
		err := dec.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Action == "pass" && event.Test != "" && !strings.Contains(event.Test, "/") {
			actual = append(actual, event.Package+"."+event.Test)
		}
	}
	slices.Sort(actual)
	want := []string{"example.com/aliases/dot.TestDot", "example.com/aliases/p.FuzzAlias", "example.com/aliases/p.FuzzBridge", "example.com/aliases/p.Test", "example.com/aliases/p.TestAlias", "example.com/aliases/p.TestBridge", "example.com/aliases/p.TestExternalAlias", "example.com/aliases/p.TestMain", "example.com/aliases/p.TestQualified"}
	if !slices.Equal(actual, want) {
		t.Fatalf("Go ran %v, want %v", actual, want)
	}
	obligations, err := DiscoverInvocation(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	var discovered []string
	selection := TestSelection{}
	seed := false
	for _, o := range obligations {
		if o.Kind == ObligationTest || o.Kind == ObligationFuzz {
			discovered = append(discovered, o.Package+"."+o.Name)
			selection[o.Package] = append(selection[o.Package], o.Name)
		}
		if o.ID() == "seed:example.com/aliases/p.FuzzAlias/committed" {
			seed = true
		}
	}
	slices.Sort(discovered)
	if !slices.Equal(discovered, actual) || !seed {
		t.Fatalf("discovery=%v, Go=%v, committed seed=%t", discovered, actual, seed)
	}
	result, err := ExecuteSelection(t.Context(), n, selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range actual {
		at := strings.LastIndex(symbol, ".")
		pkg, name := symbol[:at], symbol[at+1:]
		tr := findTest(result.Tests, pkg, name)
		if tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
			t.Fatalf("selected %s did not pass: %v", symbol, tr)
		}
	}
	if tr := findTest(result.Tests, "example.com/aliases/p", "FuzzAlias/committed"); tr == nil || tr.GetOutcome() != stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED {
		t.Fatalf("committed fuzz seed lost: %v", tr)
	}
}

// TestDiscoveryKeepsTheBuildFailureBehindAnUnresolvedHandle preserves the
// package obligation when a syntactically accepted handle cannot compile.
func TestDiscoveryKeepsTheBuildFailureBehindAnUnresolvedHandle(t *testing.T) {
	if testing.Short() {
		t.Skip("lists and executes a package with an incompatible testing handle")
	}
	stipulate.Covers(t, "REQ-go-policy-complete-obligation-identity", "REQ-policy-conservation")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":      "module example.com/badhandle\n\ngo 1.27\n",
		"bad_test.go": "package badhandle\ntype T struct{}\nfunc TestBad(*T) {}\n",
	})
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"."})
	inv := goInvocation("bad-handle", cfg)
	inv.SetTimeout(durationpb.New(time.Minute))
	n, err := NormalizeInvocation(t.Context(), dir, inv)
	if err != nil {
		t.Fatal(err)
	}
	obligations, err := DiscoverInvocation(t.Context(), n)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, o := range obligations {
		ids = append(ids, o.ID())
	}
	want := []string{"package:example.com/badhandle", "test:example.com/badhandle.TestBad"}
	if !slices.Equal(ids, want) {
		t.Fatalf("broken-package discovery = %v", ids)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", ".")
	cmd.Dir, cmd.Env = n.Dir, n.Env
	if out, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(out, []byte("build failed")) {
		t.Fatalf("incompatible handle did not remain a build failure: %v\n%s", err, out)
	}
}

// FuzzDiscoveryEntrypointSyntax varies names and declaration shapes apart
// from type resolution, which remains the compiler's responsibility.
func FuzzDiscoveryEntrypointSyntax(f *testing.F) {
	for kind := byte(0); kind < 2; kind++ {
		for shape := byte(0); shape < 14; shape++ {
			for name := byte(0); name < 8; name++ {
				f.Add(kind, shape, name)
			}
		}
	}
	f.Fuzz(func(t *testing.T, kind, shape, name byte) {
		stipulate.Covers(t, "REQ-go-policy-complete-obligation-identity")
		prefix, handle := "Test", "T"
		if kind%2 == 1 {
			prefix, handle = "Fuzz", "F"
		}
		// The first five suffixes are accepted Go test names; the other
		// three begin with a lowercase letter, including non-ASCII letters.
		suffixes := []string{"", "A", "0", "É", "𐐀", "lower", "é", "𐐨"}
		signatures := []string{"(*H)", "(x *H)", "(*bridge.H)", "(x *renamed.H)", "()", "(H)", "(*Alias)", "(a,b *H)", "(*H,*H)", "(*H) bool", "[X any](*H)", "(...*H)", "((*H))", "(*H)"}
		index := int(shape) % len(signatures)
		receiver := ""
		if index == 13 {
			receiver = "(r receiver) "
		}
		source := "package p\nfunc " + receiver + prefix + suffixes[int(name)%len(suffixes)] + strings.ReplaceAll(signatures[index], "H", handle) + " {}\n"
		file, err := parser.ParseFile(token.NewFileSet(), "shape_test.go", source, 0)
		if err != nil {
			t.Fatalf("generator produced invalid Go syntax: %v\n%s", err, source)
		}
		fd := file.Decls[0].(*ast.FuncDecl)
		want := int(name)%len(suffixes) < 5 && index < 4
		if got := syntacticTestFunc(fd, prefix, handle); got != want {
			t.Fatalf("shape admitted=%t want=%t: %s", got, want, source)
		}
	})
}
