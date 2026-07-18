package policy

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

var update = flag.Bool("update", false, "rewrite the wire fixtures under testdata")

// canonicalJSON renders a message's deterministic JSON projection: the
// ProtoJSON encoding re-serialized with sorted keys and fixed
// indentation, because protojson.Marshal deliberately randomizes its
// whitespace while consumers pin bytes.
func canonicalJSON(m proto.Message) ([]byte, error) {
	b, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// fixturePolicy populates every current TestPolicy field.
func fixturePolicy() proto.Message {
	member := &stipulatorv1.GoInvocationConfig{}
	member.SetModuleRoot("bindingsurface")
	member.SetPackages([]string{"./..."})
	member.SetToolchain("go1.26.4")
	member.SetEnvironment([]string{"GOPROXY=off"})
	member.SetEnvDeny([]string{"HTTPS_PROXY"})
	member.SetGoos("linux")
	member.SetGoarch("arm64")
	member.SetCgoEnabled(true)
	member.SetTags([]string{"integration"})
	member.SetGoflags("-trimpath")
	member.SetWorkspaceMode(stipulatorv1.GoWorkspaceMode_GO_WORKSPACE_MODE_OFF)
	member.SetModuleMode(stipulatorv1.GoModuleMode_GO_MODULE_MODE_VENDOR)
	member.SetPgo("profiles/default.pgo")
	member.SetCount(1)
	member.SetCacheMode(stipulatorv1.GoCacheMode_GO_CACHE_MODE_BYPASS)
	member.SetArgs([]string{"-quick"})
	inv1 := &stipulatorv1.PolicyInvocation{}
	inv1.SetName("member-health")
	inv1.SetTimeout(durationpb.New(600e9))
	inv1.SetGo(member)

	root := &stipulatorv1.GoInvocationConfig{}
	root.SetPackages([]string{"./...", "./stipulate/..."})
	root.SetRace(true)
	inv2 := &stipulatorv1.PolicyInvocation{}
	inv2.SetName("workspace-race")
	inv2.SetTimeout(durationpb.New(900e9))
	inv2.SetGo(root)

	p := &stipulatorv1.TestPolicy{}
	p.SetInvocations([]*stipulatorv1.PolicyInvocation{inv1, inv2})
	return p
}

// fixtureExecutionReport populates every current ExecutionReport field.
func fixtureExecutionReport() proto.Message {
	pkgOK := &stipulatorv1.PackageHealth{}
	pkgOK.SetPackage("example.com/m/ok")
	pkgOK.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_HEALTHY)
	pkgBad := &stipulatorv1.PackageHealth{}
	pkgBad.SetPackage("example.com/m/broken")
	pkgBad.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_BUILD_FAILED)
	invHealth := &stipulatorv1.InvocationHealth{}
	invHealth.SetInvocation("workspace-race")
	invHealth.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_TEST_FAILED)
	invHealth.SetPackages([]*stipulatorv1.PackageHealth{pkgOK, pkgBad})
	resolved := &stipulatorv1.GoResolvedConfig{}
	resolved.SetToolchain("go1.26.4")
	resolved.SetGoos("linux")
	resolved.SetGoarch("arm64")
	resolved.SetCgoEnabled(true)
	resolved.SetGoflags("-trimpath")
	resolved.SetGoexperiment("jsonv2")
	resolved.SetWorkspaceOn(true)
	resolved.SetRace(true)
	invHealth.SetGo(resolved)

	producer := &stipulatorv1.ProducerIdentity{}
	producer.SetInvocation("workspace-race")
	producer.SetProcessId(4242)
	producer.SetProcessOrdinal(1)
	test := &stipulatorv1.TestResult{}
	test.SetPackage("example.com/m/ok")
	test.SetTest("TestConservation/named_case")
	test.SetOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_PASSED)
	test.SetProducer(producer)
	test.SetRegistrations([]string{"REQ-a", "REQ-b"})

	completedObs := &stipulatorv1.Observation{}
	completedObs.SetProducer(producer)
	completedObs.SetPackage("example.com/m/ok")
	completed := &stipulatorv1.CompletedObservation{}
	completed.SetManifest(`{"v":1,"env":["HOME"],"paths":[{"k":"rel","p":"testdata/fixture.txt"}]}`)
	completed.SetDigest("00112233445566778899aabbccddeeff")
	completedObs.SetCompleted(completed)
	incompleteProducer := &stipulatorv1.ProducerIdentity{}
	incompleteProducer.SetInvocation("workspace-race")
	incompleteProducer.SetProcessId(4243)
	incompleteProducer.SetProcessOrdinal(2)
	incompleteObs := &stipulatorv1.Observation{}
	incompleteObs.SetProducer(incompleteProducer)
	incompleteObs.SetPackage("example.com/m/broken")
	incompleteObs.SetIncompleteReason("tests started but unfinished; the process died before its testlog flushed")

	omitted := &stipulatorv1.ObligationReport{}
	omitted.SetBackend("go")
	omitted.SetObligation("example.com/m/orphan")
	omitted.SetDisposition(stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_OMITTED)
	doubled := &stipulatorv1.ObligationReport{}
	doubled.SetBackend("go")
	doubled.SetObligation("example.com/m/ok")
	doubled.SetDisposition(stipulatorv1.ObligationDisposition_OBLIGATION_DISPOSITION_MULTIPLY_SELECTED)
	doubled.SetInvocations([]string{"member-health", "workspace-race"})

	diag := &stipulatorv1.FailureDiagnostic{}
	diag.SetInvocation("workspace-race")
	diag.SetPackage("example.com/m/broken")
	diag.SetTest("TestFlaky")
	diag.SetDisposition(stipulatorv1.HealthDisposition_HEALTH_DISPOSITION_DEGRADED)
	diag.SetOutput("test binary exited without a report\n")
	diag.SetTruncated(true)

	r := &stipulatorv1.ExecutionReport{}
	r.SetInvocations([]*stipulatorv1.InvocationHealth{invHealth})
	r.SetTests([]*stipulatorv1.TestResult{test})
	r.SetObligations([]*stipulatorv1.ObligationReport{omitted, doubled})
	r.SetDiagnostics([]*stipulatorv1.FailureDiagnostic{diag})
	r.SetObservations([]*stipulatorv1.Observation{completedObs, incompleteObs})
	return r
}

// fixtureProgressEvent populates every current ProgressEvent field.
func fixtureProgressEvent() proto.Message {
	e := &stipulatorv1.ProgressEvent{}
	e.SetPhase(stipulatorv1.Phase_PHASE_EXECUTION)
	e.SetInvocation("workspace-race")
	e.SetElapsed(durationpb.New(90500e6))
	e.SetCompleted(7)
	e.SetTotal(12)
	e.SetTerminalCause(stipulatorv1.TerminalCause_TERMINAL_CAUSE_DEADLINE)
	return e
}

// fixtureCheckResult populates every current CheckResult field.
func fixtureCheckResult() proto.Message {
	problem := &stipulatorv1.Problem{}
	problem.SetPath("docs/specs/evidence.md")
	problem.SetMessage("registration names an unbound requirement")

	binding := &stipulatorv1.BindingResult{}
	binding.SetPath(".stipulator/bindings/evidence.textproto")
	binding.SetRequirementId("REQ-x")
	binding.SetSymbol("example.com/m.TestX")
	binding.SetBackend("go")
	binding.SetRole(stipulatorv1.BindingRole_BINDING_ROLE_TESTS)
	binding.SetContentPinned(true)
	binding.SetResolution(stipulatorv1.Resolution_RESOLUTION_RESOLVED)
	binding.SetShape(stipulatorv1.ShapeState_SHAPE_STATE_MATCH)
	binding.SetTestOutcome(stipulatorv1.TestOutcome_TEST_OUTCOME_FAILED)
	binding.SetWitnessClass(stipulatorv1.WitnessClass_WITNESS_CLASS_PROPERTY)
	binding.SetRaceEnabled(true)
	verify := &stipulatorv1.VerifyReport{}
	verify.SetResults([]*stipulatorv1.BindingResult{binding})

	cov := &stipulatorv1.RequirementCoverage{}
	cov.SetId("REQ-x")
	cov.SetKind(stipulatorv1.ClauseKind_CLAUSE_KIND_BEHAVIOR)
	cov.SetKeyword(stipulatorv1.Keyword_KEYWORD_MUST)
	cov.SetBucket(stipulatorv1.Bucket_BUCKET_BROKEN)
	cov.SetReasons([]string{"bound test failed"})
	gap := &stipulatorv1.GapReport{}
	gap.SetPath(".stipulator/gaps/y.textproto")
	gap.SetRequirementId("REQ-y")
	gap.SetState(stipulatorv1.GapState_GAP_STATE_RESOLVED)
	coverage := &stipulatorv1.CoverageReport{}
	coverage.SetRequirements([]*stipulatorv1.RequirementCoverage{cov})
	coverage.SetGaps([]*stipulatorv1.GapReport{gap})
	coverage.SetViolations([]string{"REQ-x"})
	coverage.SetGatePasses(false)
	coverage.SetPolicyOverrides([]string{"behavior MUST: attestation"})

	c := &stipulatorv1.CheckResult{}
	c.SetPassed(false)
	c.SetCompileProblems([]*stipulatorv1.Problem{problem})
	c.SetExecution(fixtureExecutionReport().(*stipulatorv1.ExecutionReport))
	c.SetVerify(verify)
	c.SetCoverage(coverage)
	c.SetPruneResidue([]string{".stipulator/gaps/y.textproto"})
	return c
}

type wireFixture struct {
	name  string
	msg   func() proto.Message
	fresh func() proto.Message
}

var wireFixtures = []wireFixture{
	{"test_policy", fixturePolicy, func() proto.Message { return &stipulatorv1.TestPolicy{} }},
	{"execution_report", fixtureExecutionReport, func() proto.Message { return &stipulatorv1.ExecutionReport{} }},
	{"progress_event", fixtureProgressEvent, func() proto.Message { return &stipulatorv1.ProgressEvent{} }},
	{"check_result", fixtureCheckResult, func() proto.Message { return &stipulatorv1.CheckResult{} }},
}

// roundTrip pins one fixture's deterministic binary and canonical JSON
// encodings byte-for-byte, and that both decode back to the same message.
func roundTrip(t *testing.T, name string, msg, fresh func() proto.Message) {
	t.Helper()
	m := msg()
	binPath := filepath.Join("testdata", name+".binpb")
	jsonPath := filepath.Join("testdata", name+".json")

	det, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	cj, err := canonicalJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(binPath, det, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(jsonPath, cj, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	wantBin, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(det, wantBin) {
		t.Errorf("deterministic binary encoding drifted from %s (rerun with -update only if the change is intended wire change)", binPath)
	}
	fromBin := fresh()
	if err := proto.Unmarshal(wantBin, fromBin); err != nil {
		t.Fatalf("binary fixture does not unmarshal: %v", err)
	}
	if !proto.Equal(m, fromBin) {
		t.Error("binary fixture decodes to a different message")
	}
	reBin, err := proto.MarshalOptions{Deterministic: true}.Marshal(fromBin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reBin, wantBin) {
		t.Error("binary round trip is not byte-stable")
	}

	wantJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cj, wantJSON) {
		t.Errorf("canonical JSON drifted from %s", jsonPath)
	}
	fromJSON := fresh()
	if err := protojson.Unmarshal(wantJSON, fromJSON); err != nil {
		t.Fatalf("JSON fixture does not unmarshal: %v", err)
	}
	if !proto.Equal(m, fromJSON) {
		t.Error("JSON fixture decodes to a different message")
	}
	if !proto.Equal(fromBin, fromJSON) {
		t.Error("binary and JSON consumers decode different messages")
	}
}

func TestPolicyAndReportWireFixturesRoundTrip(t *testing.T) {
	stipulate.Covers(t, "REQ-report-policy-messages")
	for _, f := range wireFixturesNamed(t, "test_policy", "execution_report", "progress_event") {
		t.Run(f.name, func(t *testing.T) { roundTrip(t, f.name, f.msg, f.fresh) })
	}
}

func TestCheckResultWireFixtureRoundTrip(t *testing.T) {
	stipulate.Covers(t, "REQ-report-check-result")
	f := wireFixtureNamed(t, "check_result")
	roundTrip(t, f.name, f.msg, f.fresh)
}

func wireFixturesNamed(t *testing.T, names ...string) []wireFixture {
	t.Helper()
	out := make([]wireFixture, 0, len(names))
	for _, name := range names {
		out = append(out, wireFixtureNamed(t, name))
	}
	return out
}

func wireFixtureNamed(t *testing.T, name string) wireFixture {
	t.Helper()
	for _, f := range wireFixtures {
		if f.name == name {
			return f
		}
	}
	t.Fatalf("no wire fixture named %q", name)
	return wireFixture{}
}
