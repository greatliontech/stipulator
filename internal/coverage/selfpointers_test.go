package coverage

import (
	"os"
	"testing"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

// The tool's own corpus passes its own judgment: every enforcement
// pointer in these specs resolves to a tests- or proves-role binding of
// its requirement in this store (REQ-change-enforcement-pointers).
//
//gofresh:pure
func TestOwnCorpusPointersResolve(t *testing.T) {
	stipulate.Covers(t, "REQ-change-enforcement-pointers")
	fsys := os.DirFS("../..")
	spec, diags, err := compile.Compile(fsys)
	if err != nil || len(compile.Errors(diags)) > 0 {
		t.Fatalf("own corpus: %v %v", err, diags)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	rep := Evaluate(spec, &verify.Report{}, store, true, nil)
	if len(rep.DanglingPointers) != 0 {
		t.Fatalf("the tool's own pointers dangle: %v", rep.DanglingPointers)
	}
	named := 0
	for _, r := range spec.GetRequirements() {
		named += len(r.GetEnforcementPointers())
	}
	if named == 0 {
		t.Fatal("the own corpus names no pointers; the judgment ran over nothing")
	}
}
