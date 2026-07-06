package verify

import (
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// Proto renders the report as its wire message.
func (r *Report) Proto() *stipulatorv1.VerifyReport {
	out := &stipulatorv1.VerifyReport{}
	var problems []*stipulatorv1.Problem
	for _, p := range r.Problems {
		m := &stipulatorv1.Problem{}
		m.SetPath(p.Path)
		m.SetMessage(p.Message)
		problems = append(problems, m)
	}
	out.SetProblems(problems)

	var results []*stipulatorv1.BindingResult
	for _, br := range r.Results {
		results = append(results, BindingResultProto(br))
	}
	out.SetResults(results)

	var sigs []*stipulatorv1.ChangeSignature
	for _, cs := range r.Signatures {
		m := &stipulatorv1.ChangeSignature{}
		m.SetRequirementId(cs.RequirementId)
		m.SetLabel(labelProto[cs.Label])
		m.SetEvidence(cs.Evidence)
		sigs = append(sigs, m)
	}
	out.SetSignatures(sigs)

	var regs []*stipulatorv1.RegistrationResult
	for _, rr := range r.Registrations {
		m := &stipulatorv1.RegistrationResult{}
		m.SetPackage(rr.Package)
		m.SetTest(rr.Test)
		m.SetRequirementId(rr.Requirement)
		m.SetOutcome(outcomeProto[rr.Outcome])
		regs = append(regs, m)
	}
	out.SetRegistrations(regs)
	return out
}

var resolutionProto = map[Resolution]stipulatorv1.Resolution{
	Unverified:    stipulatorv1.Resolution_RESOLUTION_UNVERIFIED,
	Resolved:      stipulatorv1.Resolution_RESOLUTION_RESOLVED,
	NotFound:      stipulatorv1.Resolution_RESOLUTION_NOT_FOUND,
	GeneratedFile: stipulatorv1.Resolution_RESOLUTION_GENERATED_FILE,
}

var shapeProto = map[ShapeState]stipulatorv1.ShapeState{
	ShapeUnknown:  stipulatorv1.ShapeState_SHAPE_STATE_UNKNOWN,
	ShapeUnpinned: stipulatorv1.ShapeState_SHAPE_STATE_UNPINNED,
	ShapeMatch:    stipulatorv1.ShapeState_SHAPE_STATE_MATCH,
	ShapeMismatch: stipulatorv1.ShapeState_SHAPE_STATE_MISMATCH,
}

var outcomeProto = map[TestOutcome]stipulatorv1.TestOutcome{
	TestNotRun:  stipulatorv1.TestOutcome_TEST_OUTCOME_NOT_RUN,
	TestPassed:  stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
	TestFailed:  stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED,
	TestSkipped: stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED,
}

var labelProto = map[SignatureLabel]stipulatorv1.SignatureLabel{
	Rearchitecture: stipulatorv1.SignatureLabel_SIGNATURE_LABEL_REARCHITECTURE,
	SemanticDrift:  stipulatorv1.SignatureLabel_SIGNATURE_LABEL_SEMANTIC_DRIFT,
}

var classProto = map[WitnessClass]stipulatorv1.WitnessClass{
	ExampleWitness:  stipulatorv1.WitnessClass_WITNESS_CLASS_EXAMPLE,
	PropertyWitness: stipulatorv1.WitnessClass_WITNESS_CLASS_PROPERTY,
	AnalyzerProof:   stipulatorv1.WitnessClass_WITNESS_CLASS_ANALYZER_PROOF,
}

// BindingResultProto renders one binding row as its wire message — the
// one conversion both the verify report and the dossier use, so the two
// surfaces cannot drift.
func BindingResultProto(br BindingResult) *stipulatorv1.BindingResult {
	m := &stipulatorv1.BindingResult{}
	m.SetPath(br.Path)
	m.SetRequirementId(br.RequirementId)
	m.SetSymbol(br.Symbol)
	m.SetBackend(br.Backend)
	m.SetRole(br.Role)
	m.SetContentPinned(br.ContentPinned)
	m.SetResolution(resolutionProto[br.Resolution])
	m.SetShape(shapeProto[br.Shape])
	m.SetTestOutcome(outcomeProto[br.TestOutcome])
	if witnessRole(br.Role) {
		m.SetWitnessClass(classProto[br.WitnessClass])
	}
	m.SetRaceEnabled(br.RaceEnabled)
	return m
}
