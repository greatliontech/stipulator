package author

import (
	"fmt"
	"io/fs"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Attest records a survivor disposition on a symbol's kill-sheet: the
// named surviving mutant is attested equivalent (or accepted), with the
// reasoning. The claim is refused unless the sheet exists and the mutant
// is among its survivors — an attestation of nothing is not recorded —
// and a duplicate is refused. The attestation rides the sheet, so it is
// shed with the sheet's pins: a changed body, witness set, or operator
// set demands re-judgment.
func Attest(fsys fs.FS, symbol, position, operator, reason string) (*Update, error) {
	if symbol == "" || position == "" || operator == "" {
		return nil, fmt.Errorf("a symbol, position, and operator are required")
	}
	if reason == "" {
		return nil, fmt.Errorf("an attestation without reasoning is not a disposition; give --reason")
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	for _, hf := range store.Hardening {
		for _, rec := range hf.Set.GetRecords() {
			if rec.GetSymbol() != symbol {
				continue
			}
			survives := false
			for _, s := range rec.GetSurvivors() {
				if s.GetPosition() == position && s.GetOperator() == operator {
					survives = true
				}
			}
			if !survives {
				return nil, fmt.Errorf("%s has no survivor at %s (%s); only a surviving mutant can be attested", symbol, position, operator)
			}
			for _, a := range rec.GetAttested() {
				if a.GetPosition() == position && a.GetOperator() == operator {
					return nil, fmt.Errorf("survivor %s (%s) is already attested", position, operator)
				}
			}
			ma := &stipulatorv1.MutationAttestation{}
			ma.SetPosition(position)
			ma.SetOperator(operator)
			ma.SetReason(reason)
			rec.SetAttested(append(rec.GetAttested(), ma))
			return &Update{Path: hf.Path, Content: records.RenderHardening(hf.Set.GetRecords())}, nil
		}
	}
	return nil, fmt.Errorf("no kill-sheet records %s; run harden first", symbol)
}
