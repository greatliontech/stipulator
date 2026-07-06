package author

import (
	"fmt"
	"io/fs"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// AttestRequirement authors the weakest evidence (REQ-evidence-ladder):
// a reason-carrying voucher for one requirement, content-pinned so a
// moved requirement re-stales it. Refused without a reason, for
// requirements outside the corpus, and as a duplicate — one attestation
// per requirement; re-judging means replacing, not accreting.
func AttestRequirement(fsys fs.FS, requirement, reason string) (*Update, error) {
	if reason == "" {
		return nil, fmt.Errorf("an attestation without reasoning is not evidence; give --reason")
	}
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, err
	}
	contentHash := ""
	for _, r := range spec.GetRequirements() {
		if r.GetId() == requirement {
			contentHash = r.GetContentHash()
		}
	}
	if contentHash == "" {
		return nil, fmt.Errorf("requirement %s is not in the corpus", requirement)
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			if a.GetRequirementId() == requirement {
				return nil, fmt.Errorf("%s is already attested in %s; replace it deliberately, never accrete", requirement, af.Path)
			}
		}
	}
	a := &stipulatorv1.RequirementAttestation{}
	a.SetRequirementId(requirement)
	a.SetContentHash(contentHash)
	a.SetReason(reason)
	set := &stipulatorv1.AttestationSet{}
	set.SetAttestations([]*stipulatorv1.RequirementAttestation{a})
	return &Update{Path: records.AttestationPath(requirement), Content: records.RenderAttestations(set)}, nil
}
