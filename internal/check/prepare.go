package check

import (
	"fmt"
	"io/fs"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Prepared is everything an operation decides from the inputs it
// already holds, gathered before its first child process: the compiled
// corpus with its diagnostics, the committed records, the manifest and
// the coverage policy it declares, and the record-only half of
// verification. Every refusal these inputs decide fires here, so a
// caller that reaches the accepted policy or a toolchain query has
// already passed them (REQ-check-preparation).
type Prepared struct {
	Spec        *stipulatorv1.Spec
	Diagnostics []compile.Diagnostic
	Store       *records.Store
	Manifest    *stipulatorv1.Manifest
	// Coverage is the manifest's coverage policy — a duplicated cell
	// refuses at manifest read, never after a witness run.
	Coverage *coverage.Policy
	// Hygiene is the record-only half of verification: non-empty means
	// verification cannot pass whatever a witness run would say, so no
	// witness executes (verify.Hygiene).
	Hygiene []verify.Problem
}

// CompileProblems renders the corpus's compile faults — the errors and
// the remedies beside them — as problems; empty means the corpus
// compiled.
func (p *Prepared) CompileProblems() []*stipulatorv1.Problem {
	faults := compile.Faults(p.Diagnostics)
	if len(faults) == 0 {
		return nil
	}
	problems := make([]*stipulatorv1.Problem, 0, len(faults))
	for _, d := range faults {
		pr := &stipulatorv1.Problem{}
		pr.SetPath(fmt.Sprintf("%s:%d", d.Document, d.Line))
		msg := d.Message
		if d.Remedy {
			msg = "remedy: " + msg
		}
		pr.SetMessage(msg)
		problems = append(problems, pr)
	}
	return problems
}

// Prepare reads and judges the operation's held inputs in refusal
// order: the corpus compiles (its errors are the first verdict, and
// nothing below is read past them), the records load, the manifest's
// coverage policy derives, and the records' hygiene is judged. An error
// is operational — a read fault says nothing about the tree.
func Prepare(fsys fs.FS) (*Prepared, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	p := &Prepared{Spec: spec, Diagnostics: diags}
	if len(compile.Errors(diags)) > 0 {
		return p, nil
	}
	if p.Store, err = records.Load(fsys); err != nil {
		return nil, err
	}
	if p.Manifest, err = corpus.LoadManifest(fsys); err != nil {
		return nil, err
	}
	if p.Coverage, err = coverage.PolicyFromManifest(p.Manifest); err != nil {
		return nil, err
	}
	p.Hygiene = verify.Hygiene(spec, p.Store)
	return p, nil
}

// KnownIDs refuses requirement identifiers the corpus does not declare
// — at request parse, before any evidence is gathered: a typo must
// never read as an empty scope that executes nothing and passes.
func KnownIDs(spec *stipulatorv1.Spec, ids []string) error {
	known := records.HashesOf(spec)
	var unknown []string
	for _, id := range ids {
		if !known.Known(id) {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown requirement identifier(s): %s (gate view=full lists the corpus's requirement identifiers)", strings.Join(unknown, ", "))
	}
	return nil
}
