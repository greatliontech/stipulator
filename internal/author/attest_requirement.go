package author

import (
	"fmt"
	"io/fs"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// AttestRequirement authors the weakest evidence (REQ-evidence-ladder):
// a reason-carrying voucher for one requirement, content-pinned so a
// moved requirement re-stales it. Refused without a reason and for
// requirements outside the corpus. One judgment per requirement:
// attesting again replaces it in place, and the prior record is returned
// so the superseded reasoning is surfaced, never silently overwritten.
func AttestRequirement(fsys fs.FS, requirement, reason string) (*Update, *stipulatorv1.RequirementAttestation, error) {
	if reason == "" {
		return nil, nil, fmt.Errorf("an attestation without reasoning is not evidence; give --reason")
	}
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, nil, err
	}
	contentHash := ""
	for _, r := range spec.GetRequirements() {
		if r.GetId() == requirement {
			contentHash = r.GetContentHash()
		}
	}
	if contentHash == "" {
		return nil, nil, fmt.Errorf("requirement %s is not in the corpus", requirement)
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	// Two passes: locate the judgment's file first, then keep that
	// file's other records — a single stream-order pass would drop
	// unrelated records scanned before the match.
	target := records.AttestationPath(requirement)
	var prior *stipulatorv1.RequirementAttestation
	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			if a.GetRequirementId() == requirement {
				// Replace in place, at the record's existing file.
				target = af.Path
				prior = a
			}
		}
	}
	var keep []*stipulatorv1.RequirementAttestation
	for _, af := range store.Attestations {
		if af.Path != target {
			continue
		}
		for _, a := range af.Set.GetAttestations() {
			if a.GetRequirementId() != requirement {
				keep = append(keep, a)
			}
		}
	}
	a := &stipulatorv1.RequirementAttestation{}
	a.SetRequirementId(requirement)
	a.SetContentHash(contentHash)
	a.SetReason(reason)
	set := &stipulatorv1.AttestationSet{}
	set.SetAttestations(append(keep, a))
	return &Update{Path: target, Content: records.RenderAttestations(set)}, prior, nil
}

// RetractAttestation withdraws a requirement's judgment: the record is
// removed (the file deleted when it holds nothing else), and the
// retracted reasoning is returned for surfacing. Retracting nothing is an
// error.
func RetractAttestation(fsys fs.FS, requirement string) (*Update, *stipulatorv1.RequirementAttestation, error) {
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	for _, af := range store.Attestations {
		var retracted *stipulatorv1.RequirementAttestation
		var keep []*stipulatorv1.RequirementAttestation
		for _, a := range af.Set.GetAttestations() {
			if a.GetRequirementId() == requirement {
				retracted = a
				continue
			}
			keep = append(keep, a)
		}
		if retracted == nil {
			continue
		}
		if len(keep) == 0 {
			return &Update{Path: af.Path, Content: nil}, retracted, nil
		}
		set := &stipulatorv1.AttestationSet{}
		set.SetAttestations(keep)
		return &Update{Path: af.Path, Content: records.RenderAttestations(set)}, retracted, nil
	}
	return nil, nil, fmt.Errorf("no attestation records %s; nothing to retract", requirement)
}
