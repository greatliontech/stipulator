// Package dossier assembles per-requirement orientation views: the one
// call answering "tell me everything about REQ-X" — clause, coverage,
// gap, attestation, and bindings — so no consumer needs to know
// the record stores' file layout (REQ-context-dossier). Assembly only:
// every fact comes from the compiled corpus, the verification report, the
// coverage evaluation, or the record stores, computed by their owners.
package dossier

import (
	"fmt"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/coverage"
	"github.com/greatliontech/stipulator/internal/facts"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// Build assembles one dossier per requested id, in request order. An id
// not in the corpus is an error naming it exactly.
func Build(spec *stipulatorv1.Spec, vr *verify.Report, cr *coverage.Report, store *records.Store, ids []string) ([]*stipulatorv1.Dossier, error) {
	gapStates := map[string]coverage.GapState{}
	for _, g := range cr.Gaps {
		gapStates[g.RequirementId] = g.State
	}
	reqs := map[string]*stipulatorv1.Requirement{}
	for _, r := range spec.GetRequirements() {
		reqs[r.GetId()] = r
	}
	rows := map[string]coverage.Requirement{}
	for _, rc := range cr.Requirements {
		rows[rc.Id] = rc
	}
	bindings := map[string][]verify.BindingResult{}
	for _, br := range vr.Results {
		bindings[br.RequirementId] = append(bindings[br.RequirementId], br)
	}
	gaps := map[string]*stipulatorv1.Gap{}
	for _, gf := range store.Gaps {
		gaps[gf.Gap.GetRequirementId()] = gf.Gap
	}
	attestations := map[string]*stipulatorv1.RequirementAttestation{}
	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			if _, dup := attestations[a.GetRequirementId()]; !dup {
				attestations[a.GetRequirementId()] = a
			}
		}
	}
	var out []*stipulatorv1.Dossier
	for _, id := range ids {
		req, ok := reqs[id]
		if !ok {
			return nil, fmt.Errorf("requirement %q is not in the corpus", id)
		}
		d := &stipulatorv1.Dossier{}
		d.SetRequirement(req)
		if row, ok := rows[id]; ok {
			rc := &stipulatorv1.RequirementCoverage{}
			rc.SetId(row.Id)
			rc.SetKind(row.Kind)
			rc.SetKeyword(row.Keyword)
			rc.SetBucket(coverage.BucketProto(row.Bucket))
			rc.SetReasons(row.Reasons)
			d.SetCoverage(rc)
		}
		if g, ok := gaps[id]; ok {
			d.SetGap(g)
			d.SetGapState(coverage.GapStateProto(gapStates[id]))
		}
		if a, ok := attestations[id]; ok {
			d.SetAttestation(a)
		}
		brs := bindings[id]
		var wire []*stipulatorv1.BindingResult
		for _, br := range brs {
			wire = append(wire, verify.BindingResultProto(br))
		}
		d.SetBindings(wire)

		seeds, err := facts.Seeds(spec, store, []string{id})
		if err != nil {
			return nil, err
		}
		var ss []*stipulatorv1.Seed
		for _, s := range seeds {
			m := &stipulatorv1.Seed{}
			m.SetRequirementId(s.Requirement)
			m.SetBackend(s.Backend)
			m.SetSymbol(s.Symbol)
			m.SetRole(s.Role)
			ss = append(ss, m)
		}
		d.SetSeeds(ss)
		out = append(out, d)
	}
	return out, nil
}
