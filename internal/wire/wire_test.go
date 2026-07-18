package wire

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// genCheckResult draws an arbitrary CheckResult across the check
// surface: every top-level field — the execution report with its
// obligation findings and the verification report with binding rows,
// registrations, signatures, and problems included — both
// observation-evidence arms, the resolved-config arm, and enum values
// from every disposition, bucket, resolution, shape state, witness
// class, and signature label, so surface equivalence is checked over
// field shapes rather than one hand-picked example.
func genCheckResult(rt *rapid.T) *stipulatorv1.CheckResult {
	ident := rapid.StringMatching(`[A-Za-z][A-Za-z0-9./_-]{0,24}`)
	text := rapid.String()
	dispositions := []stipulatorv1.HealthDisposition{
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TIMEOUT,
		stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED,
	}
	outcomes := []stipulatorv1.TestOutcome{
		stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED,
		stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED,
		stipulatorv1.TestOutcome_TEST_OUTCOME_SKIPPED,
	}
	buckets := []stipulatorv1.Bucket{
		stipulatorv1.Bucket_BUCKET_UNCOVERED,
		stipulatorv1.Bucket_BUCKET_STALE,
		stipulatorv1.Bucket_BUCKET_BROKEN,
		stipulatorv1.Bucket_BUCKET_COVERED,
		stipulatorv1.Bucket_BUCKET_EXEMPT,
		stipulatorv1.Bucket_BUCKET_ATTESTED,
	}

	problem := func(label string) *stipulatorv1.Problem {
		p := &stipulatorv1.Problem{}
		p.SetPath(ident.Draw(rt, label+"-path"))
		p.SetMessage(text.Draw(rt, label+"-message"))
		return p
	}

	res := &stipulatorv1.CheckResult{}
	res.SetPassed(rapid.Bool().Draw(rt, "passed"))
	res.SetTestsExecuted(rapid.Int32Range(0, 1<<20).Draw(rt, "executed"))
	res.SetTestsUncacheable(rapid.Int32Range(0, 1<<20).Draw(rt, "uncacheable"))
	res.SetWitnessPublicationDegraded(text.Draw(rt, "degraded"))
	res.SetPruneResidue(rapid.SliceOfN(ident, 0, 3).Draw(rt, "residue"))
	if rapid.Bool().Draw(rt, "has-compile-problems") {
		res.SetCompileProblems([]*stipulatorv1.Problem{problem("compile")})
	}
	if rapid.Bool().Draw(rt, "has-policy-problem") {
		res.SetPolicyProblem(problem("policy"))
	}

	if rapid.Bool().Draw(rt, "has-execution") {
		producer := &stipulatorv1.ProducerIdentity{}
		producer.SetInvocation(ident.Draw(rt, "producer-invocation"))
		producer.SetProcessId(rapid.Int64Range(0, 1<<31).Draw(rt, "pid"))
		producer.SetProcessOrdinal(rapid.Int32Range(0, 64).Draw(rt, "ordinal"))

		pkg := &stipulatorv1.PackageHealth{}
		pkg.SetPackage(ident.Draw(rt, "package"))
		pkg.SetDisposition(rapid.SampledFrom(dispositions).Draw(rt, "package-disposition"))
		inv := &stipulatorv1.InvocationHealth{}
		inv.SetInvocation(ident.Draw(rt, "invocation"))
		inv.SetDisposition(rapid.SampledFrom(dispositions).Draw(rt, "invocation-disposition"))
		inv.SetPackages([]*stipulatorv1.PackageHealth{pkg})
		if rapid.Bool().Draw(rt, "has-resolved") {
			cfg := &stipulatorv1.GoResolvedConfig{}
			cfg.SetToolchain(ident.Draw(rt, "toolchain"))
			cfg.SetRace(rapid.Bool().Draw(rt, "race"))
			inv.SetGo(cfg)
		}

		test := &stipulatorv1.TestResult{}
		test.SetPackage(pkg.GetPackage())
		test.SetTest(ident.Draw(rt, "test"))
		test.SetOutcome(rapid.SampledFrom(outcomes).Draw(rt, "outcome"))
		test.SetProducer(producer)
		test.SetRegistrations(rapid.SliceOfN(ident, 0, 3).Draw(rt, "registrations"))

		diag := &stipulatorv1.FailureDiagnostic{}
		diag.SetInvocation(inv.GetInvocation())
		diag.SetPackage(pkg.GetPackage())
		diag.SetDisposition(rapid.SampledFrom(dispositions).Draw(rt, "diag-disposition"))
		diag.SetOutput(text.Draw(rt, "diag-output"))
		diag.SetTruncated(rapid.Bool().Draw(rt, "truncated"))

		obs := &stipulatorv1.Observation{}
		obs.SetProducer(producer)
		obs.SetPackage(pkg.GetPackage())
		if rapid.Bool().Draw(rt, "obs-completed") {
			completed := &stipulatorv1.CompletedObservation{}
			completed.SetManifest(text.Draw(rt, "manifest"))
			completed.SetDigest(ident.Draw(rt, "digest"))
			obs.SetCompleted(completed)
		} else {
			obs.SetIncompleteReason(text.Draw(rt, "incomplete-reason"))
		}

		ex := &stipulatorv1.ExecutionReport{}
		ex.SetInvocations([]*stipulatorv1.InvocationHealth{inv})
		ex.SetTests([]*stipulatorv1.TestResult{test})
		ex.SetDiagnostics([]*stipulatorv1.FailureDiagnostic{diag})
		ex.SetObservations([]*stipulatorv1.Observation{obs})
		if rapid.Bool().Draw(rt, "has-obligations") {
			oblig := &stipulatorv1.ObligationReport{}
			oblig.SetBackend(ident.Draw(rt, "oblig-backend"))
			oblig.SetObligation(ident.Draw(rt, "obligation"))
			oblig.SetDisposition(rapid.SampledFrom([]stipulatorv1.ObligationDisposition{
				stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_OMITTED,
				stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_MULTIPLY_SELECTED,
			}).Draw(rt, "oblig-disposition"))
			oblig.SetInvocations(rapid.SliceOfN(ident, 0, 2).Draw(rt, "oblig-invocations"))
			ex.SetObligations([]*stipulatorv1.ObligationReport{oblig})
		}
		res.SetExecution(ex)
	}

	if rapid.Bool().Draw(rt, "has-verify") {
		binding := func(label string) *stipulatorv1.BindingResult {
			b := &stipulatorv1.BindingResult{}
			b.SetPath(ident.Draw(rt, label+"-path"))
			b.SetRequirementId(ident.Draw(rt, label+"-req"))
			b.SetSymbol(ident.Draw(rt, label+"-symbol"))
			b.SetBackend(ident.Draw(rt, label+"-backend"))
			b.SetRole(rapid.SampledFrom([]stipulatorv1.BindingRole{
				stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS,
				stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
				stipulatorv1.BindingRole_BINDING_ROLE_PROVES,
			}).Draw(rt, label+"-role"))
			b.SetContentPinned(rapid.Bool().Draw(rt, label+"-pinned"))
			b.SetResolution(rapid.SampledFrom([]stipulatorv1.Resolution{
				stipulatorv1.Resolution_RESOLUTION_UNVERIFIED,
				stipulatorv1.Resolution_RESOLUTION_RESOLVED,
				stipulatorv1.Resolution_RESOLUTION_NOT_FOUND,
				stipulatorv1.Resolution_RESOLUTION_GENERATED_FILE,
			}).Draw(rt, label+"-resolution"))
			b.SetShape(rapid.SampledFrom([]stipulatorv1.ShapeState{
				stipulatorv1.ShapeState_SHAPE_STATE_UNKNOWN,
				stipulatorv1.ShapeState_SHAPE_STATE_UNPINNED,
				stipulatorv1.ShapeState_SHAPE_STATE_MATCH,
				stipulatorv1.ShapeState_SHAPE_STATE_MISMATCH,
			}).Draw(rt, label+"-shape"))
			b.SetTestOutcome(rapid.SampledFrom(outcomes).Draw(rt, label+"-outcome"))
			b.SetWitnessClass(rapid.SampledFrom([]stipulatorv1.WitnessClass{
				stipulatorv1.WitnessClass_WITNESS_CLASS_EXAMPLE,
				stipulatorv1.WitnessClass_WITNESS_CLASS_PROPERTY,
				stipulatorv1.WitnessClass_WITNESS_CLASS_ANALYZER_PROOF,
			}).Draw(rt, label+"-witness-class"))
			b.SetRaceEnabled(rapid.Bool().Draw(rt, label+"-race"))
			return b
		}
		reg := &stipulatorv1.RegistrationResult{}
		reg.SetPackage(ident.Draw(rt, "reg-package"))
		reg.SetTest(ident.Draw(rt, "reg-test"))
		reg.SetRequirementId(ident.Draw(rt, "reg-req"))
		reg.SetOutcome(rapid.SampledFrom(outcomes).Draw(rt, "reg-outcome"))
		sig := &stipulatorv1.ChangeSignature{}
		sig.SetRequirementId(ident.Draw(rt, "sig-req"))
		sig.SetLabel(rapid.SampledFrom([]stipulatorv1.SignatureLabel{
			stipulatorv1.SignatureLabel_SIGNATURE_LABEL_REARCHITECTURE,
			stipulatorv1.SignatureLabel_SIGNATURE_LABEL_SEMANTIC_DRIFT,
		}).Draw(rt, "sig-label"))
		sig.SetEvidence(rapid.SliceOfN(text, 0, 2).Draw(rt, "sig-evidence"))
		vr := &stipulatorv1.VerifyReport{}
		if rapid.Bool().Draw(rt, "has-verify-problems") {
			vr.SetProblems([]*stipulatorv1.Problem{problem("verify")})
		}
		vr.SetResults([]*stipulatorv1.BindingResult{binding("row-a"), binding("row-b")})
		vr.SetRegistrations([]*stipulatorv1.RegistrationResult{reg})
		vr.SetSignatures([]*stipulatorv1.ChangeSignature{sig})
		res.SetVerify(vr)
	}

	if rapid.Bool().Draw(rt, "has-coverage") {
		cov := &stipulatorv1.RequirementCoverage{}
		cov.SetId(ident.Draw(rt, "req-id"))
		cov.SetBucket(rapid.SampledFrom(buckets).Draw(rt, "bucket"))
		cov.SetReasons(rapid.SliceOfN(text, 0, 2).Draw(rt, "reasons"))
		coverage := &stipulatorv1.CoverageReport{}
		coverage.SetRequirements([]*stipulatorv1.RequirementCoverage{cov})
		coverage.SetViolations(rapid.SliceOfN(ident, 0, 2).Draw(rt, "violations"))
		coverage.SetGatePasses(rapid.Bool().Draw(rt, "gate-passes"))
		res.SetCoverage(coverage)
	}
	return res
}

// TestCheckSurfacesRenderEquivalentFacts is the cross-mode equivalence
// property for the check surfaces: for an arbitrary CheckResult, the
// CLI's --json bytes (CanonicalJSON) and the MCP structured result
// (StructuredContent) both decode — strictly — back to the same message,
// so the two machine surfaces carry identical facts.
func TestCheckSurfacesRenderEquivalentFacts(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result", "REQ-mcp-tools")
	rapid.Check(t, func(rt *rapid.T) {
		res := genCheckResult(rt)

		cli, err := CanonicalJSON(res)
		if err != nil {
			rt.Fatal(err)
		}
		structured, err := StructuredContent(res)
		if err != nil {
			rt.Fatal(err)
		}
		mcpBytes, err := json.Marshal(structured)
		if err != nil {
			rt.Fatal(err)
		}

		fromCLI := &stipulatorv1.CheckResult{}
		if err := protojson.Unmarshal(cli, fromCLI); err != nil {
			rt.Fatalf("CLI projection is not a strict CheckResult: %v\n%s", err, cli)
		}
		fromMCP := &stipulatorv1.CheckResult{}
		if err := protojson.Unmarshal(mcpBytes, fromMCP); err != nil {
			rt.Fatalf("MCP projection is not a strict CheckResult: %v\n%s", err, mcpBytes)
		}
		if !proto.Equal(fromCLI, res) {
			rt.Fatalf("CLI projection lost facts:\n%s", cli)
		}
		if !proto.Equal(fromCLI, fromMCP) {
			rt.Fatalf("CLI and MCP surfaces decode different messages:\ncli: %s\nmcp: %s", cli, mcpBytes)
		}
	})
}

// TestCanonicalJSONIsByteDeterministic pins the canonical projection's
// bytes: two renders of one message are identical, keys sorted, trailing
// newline present — the property machine consumers pin.
func TestCanonicalJSONIsByteDeterministic(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	rapid.Check(t, func(rt *rapid.T) {
		res := genCheckResult(rt)
		a, err := CanonicalJSON(res)
		if err != nil {
			rt.Fatal(err)
		}
		b, err := CanonicalJSON(res)
		if err != nil {
			rt.Fatal(err)
		}
		if string(a) != string(b) {
			rt.Fatalf("canonical projection not deterministic:\n%s\n%s", a, b)
		}
		if a[len(a)-1] != '\n' {
			rt.Fatal("canonical projection lacks the trailing newline")
		}
	})
}
