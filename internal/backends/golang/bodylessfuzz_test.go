package golang

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// A bodyless fuzz declaration can be implemented by a linked function.
// The actual harness is the authority on its runnability; requiring a Go
// body would silently lower its evidence class.
func TestBodylessFuzzDeclarationKeepsHarnessClass(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a linked fuzz declaration and loads its source")
	}
	stipulate.Covers(t, "REQ-go-witness-class", "REQ-mcp-explain")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/bodyless\n\ngo 1.27\n",
		"p.go":      "package bodyless\nimport \"testing\"\nfunc implementation(f *testing.F) { f.Add(1); f.Fuzz(func(*testing.T, int) {}) }\nfunc testImpl(*testing.T) {}\n",
		"p_test.go": "package bodyless\nimport (\"testing\"; _ \"unsafe\")\n//go:linkname FuzzForward example.com/bodyless.implementation\nfunc FuzzForward(*testing.F)\n//go:linkname TestForward example.com/bodyless.testImpl\nfunc TestForward(*testing.T)\n",
	})
	cmd := exec.CommandContext(t.Context(), "go", "test", "-json", "-count=1", "-run=^(FuzzForward|TestForward)$", ".")
	cmd.Dir, cmd.Env = dir, os.Environ()
	data, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(data, []byte(`"Test":"FuzzForward"`)) || !bytes.Contains(data, []byte(`"Test":"TestForward"`)) {
		t.Fatalf("Go did not execute the linked fuzz target: %v\n%s", err, data)
	}
	b, err := newContext(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if class, why := b.WitnessClassVerdict("example.com/bodyless.FuzzForward"); class != verify.PropertyWitness {
		t.Fatalf("linked fuzz class = %v, %s", class, why)
	}
	if class, why := b.WitnessClassVerdict("example.com/bodyless.TestForward"); class != verify.ExampleWitness || why != "no Go body to inspect" {
		t.Fatalf("linked test class = %v, %s", class, why)
	}
	for _, row := range []struct{ name, arm, pos string }{
		{"FuzzForward", "not seeded (property)", "p_test.go:4"},
		{"TestForward", "not seeded (example)", "p_test.go:6"},
	} {
		chain, _, err := b.explainWitness("example.com/bodyless." + row.name)
		if err != nil || chain.Arm != row.arm || len(chain.Links) != 1 || chain.Links[0].Pos != row.pos || chain.Links[0].Clause != "no Go body to inspect; linked implementation not inspected" {
			t.Fatalf("linked declaration explanation: %+v, %v", chain, err)
		}
	}
}
