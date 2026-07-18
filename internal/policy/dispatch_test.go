package policy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
)

// recordingBackend records what the dispatch seam hands it.
type recordingBackend struct {
	invocations []string
	payloads    []proto.Message
	err         error
}

func (b *recordingBackend) ValidateInvocation(invocation string, payload proto.Message) error {
	b.invocations = append(b.invocations, invocation)
	b.payloads = append(b.payloads, payload)
	return b.err
}

func mkGoInvocation(name, moduleRoot string, timeout time.Duration) *stipulatorv1.PolicyInvocation {
	cfg := &stipulatorv1.GoInvocationConfig{}
	if moduleRoot != "" {
		cfg.SetModuleRoot(moduleRoot)
	}
	cfg.SetPackages([]string{"./..."})
	cfg.SetRace(true)
	inv := &stipulatorv1.PolicyInvocation{}
	inv.SetName(name)
	inv.SetTimeout(durationpb.New(timeout))
	inv.SetGo(cfg)
	return inv
}

// TestPolicyDispatchRoutesPayloadsToNamedBackend pins the dispatch seam:
// each invocation's typed payload reaches the backend named by its payload
// case, while the caller receives only canonical invocation identity,
// backend name, and timeout — never the payload.
//
//gofresh:pure
func TestPolicyDispatchRoutesPayloadsToNamedBackend(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-backend-neutral")
	rb := &recordingBackend{}
	p := mkPolicy(
		mkGoInvocation("member-race", "member", 10*time.Minute),
		mkGoInvocation("workspace-race", "", 15*time.Minute),
	)
	invs, err := Dispatch(p, map[string]Backend{"go": rb})
	if err != nil {
		t.Fatal(err)
	}
	want := []Invocation{
		{Name: "member-race", Backend: "go", Timeout: 10 * time.Minute},
		{Name: "workspace-race", Backend: "go", Timeout: 15 * time.Minute},
	}
	if len(invs) != len(want) {
		t.Fatalf("invocations = %d, want %d", len(invs), len(want))
	}
	for i := range want {
		if invs[i] != want[i] {
			t.Errorf("invocation %d = %+v, want %+v", i, invs[i], want[i])
		}
	}
	if len(rb.payloads) != 2 {
		t.Fatalf("backend saw %d payloads, want 2", len(rb.payloads))
	}
	if got := rb.invocations; got[0] != "member-race" || got[1] != "workspace-race" {
		t.Errorf("backend saw invocations %v, want canonical identities", got)
	}
	cfg, ok := rb.payloads[0].(*stipulatorv1.GoInvocationConfig)
	if !ok {
		t.Fatalf("backend received %T, want its own typed payload", rb.payloads[0])
	}
	if cfg.GetModuleRoot() != "member" {
		t.Errorf("payload module_root = %q, want %q", cfg.GetModuleRoot(), "member")
	}
}

// TestPolicyDispatchRejectsUnsupportedBackend pins the unsupported-backend
// refusal: a payload case no registered backend claims rejects the policy
// whole, naming the invocation and the unclaimed case.
//
//gofresh:pure
func TestPolicyDispatchRejectsUnsupportedBackend(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-backend-neutral")
	p := mkPolicy(mkGoInvocation("race", "", time.Minute))
	invs, err := Dispatch(p, map[string]Backend{})
	if err == nil {
		t.Fatal("policy with an unclaimed payload case accepted")
	}
	if invs != nil {
		t.Fatal("refusal returned partial invocations")
	}
	for _, part := range []string{`"race"`, `"go"`, "no registered backend"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error = %q, want it to contain %s", err, part)
		}
	}
}

// TestPolicyDispatchPropagatesBackendRefusal pins that a payload the
// claiming backend refuses rejects the policy whole, attributed to its
// invocation.
//
//gofresh:pure
func TestPolicyDispatchPropagatesBackendRefusal(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-backend-neutral")
	rb := &recordingBackend{err: errors.New("module_root escapes")}
	_, err := Dispatch(mkPolicy(mkGoInvocation("race", "", time.Minute)), map[string]Backend{"go": rb})
	if err == nil {
		t.Fatal("refused payload accepted")
	}
	if !strings.Contains(err.Error(), `"race"`) || !strings.Contains(err.Error(), "module_root escapes") {
		t.Errorf("error = %q, want the invocation and the backend's refusal", err)
	}
	// A non-canonical envelope never reaches any backend.
	rb2 := &recordingBackend{}
	bad := mkPolicy(mkGoInvocation("", "", time.Minute))
	if _, err := Dispatch(bad, map[string]Backend{"go": rb2}); err == nil {
		t.Fatal("non-canonical policy dispatched")
	}
	if len(rb2.payloads) != 0 {
		t.Error("non-canonical policy reached a backend")
	}
}

// TestPolicyLoadReadsCommittedRecord pins the loading seam: the record is
// read from its fixed location, strict-parsed, and dispatched; an absent
// record is an explicit error, never an assumed policy.
func TestPolicyLoadReadsCommittedRecord(t *testing.T) {
	stipulate.Covers(t, "REQ-policy-explicit")
	dir := t.TempDir()
	rb := &recordingBackend{}
	if _, _, err := Load(dir, map[string]Backend{"go": rb}); err == nil ||
		!strings.Contains(err.Error(), "no accepted test policy") {
		t.Fatalf("absent record: err = %v, want the explicit-policy refusal", err)
	}

	p := mkPolicy(mkGoInvocation("race", "", time.Minute))
	rendered, err := Render(p)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, filepath.FromSlash(Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, invs, err := Load(dir, map[string]Backend{"go": rb})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(loaded, p) {
		t.Error("loaded policy differs from the written record")
	}
	if len(invs) != 1 || invs[0].Backend != "go" || invs[0].Name != "race" {
		t.Errorf("dispatched invocations = %+v", invs)
	}
}
