package author

import (
	"fmt"
	"io/fs"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/coverage"
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
	contentHash, sourceHash := "", ""
	var kind stipulatorv1.ClauseKind
	var keyword stipulatorv1.Keyword
	for _, r := range spec.GetRequirements() {
		if r.GetId() == requirement {
			contentHash, sourceHash = r.GetContentHash(), r.GetSourceHash()
			kind, keyword = r.GetKind(), r.GetKeyword()
		}
	}
	if contentHash == "" {
		return nil, nil, fmt.Errorf("requirement %s is not in the corpus", requirement)
	}
	// Born-valid, like the bind verb's proves-role refusal: an
	// attestation on a cell whose policy can never render the attested
	// bucket is refused at write time with the cell's real demand, not
	// recorded to rot as an unexplained red (REQ-change-remediation).
	manifest, err := corpus.LoadManifest(fsys)
	if err != nil {
		return nil, nil, err
	}
	pol, err := coverage.PolicyFromManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	if !coverage.AdmitsAttestation(pol, kind, keyword) {
		// The need carries its remedy: on the invariant cell that is the
		// reclassification, so the author is sent to the one edit that
		// renders the decision rather than left to hunt for a gap
		// (REQ-change-remediation).
		hint := ""
		if !pol.Overridden(kind, keyword) {
			if h := coverage.KindHint(kind, keyword); h != "" {
				hint = "; " + h
			}
		}
		return nil, nil, fmt.Errorf("the (%s, %s) cell never admits attestation — it %s; an attestation that can never render is refused, not recorded%s",
			strings.ToLower(strings.TrimPrefix(kind.String(), "CLAUSE_KIND_")),
			strings.ReplaceAll(strings.TrimPrefix(keyword.String(), "KEYWORD_"), "_", " "),
			coverage.RequiredEvidence(pol, kind, keyword), hint)
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
	a.SetSourceHash(sourceHash)
	a.SetReason(reason)
	set := &stipulatorv1.AttestationSet{}
	set.SetAttestations(append(keep, a))
	// The file's leading header survives the rewrite, as every
	// machine-owned record file's does; an absent file takes the default.
	content, err := records.RenderAttestationFile(records.AttestationFile{Path: target, Raw: rawOf(store, target), Set: set})
	if err != nil {
		return nil, nil, err
	}
	up := &Update{Path: target, Content: content}
	stampPrior(store, up)
	return up, prior, nil
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
			up := &Update{Path: af.Path, Content: nil}
			stampPrior(store, up)
			return up, retracted, nil
		}
		set := &stipulatorv1.AttestationSet{}
		set.SetAttestations(keep)
		content, err := records.RenderAttestationFile(records.AttestationFile{Path: af.Path, Raw: af.Raw, Set: set})
		if err != nil {
			return nil, nil, err
		}
		up := &Update{Path: af.Path, Content: content}
		stampPrior(store, up)
		return up, retracted, nil
	}
	return nil, nil, fmt.Errorf("no attestation records %s; nothing to retract", requirement)
}

// rawOf is the loaded content of one attestation file, nil when the
// store holds no file at that path.
func rawOf(store *records.Store, path string) []byte {
	for _, af := range store.Attestations {
		if af.Path == path {
			return af.Raw
		}
	}
	return nil
}
