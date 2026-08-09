package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
)

// The policy's reviewed vouch set reaches the capture engine: a real
// version-pinned dependency culprit (protobuf's global registries)
// downgrades an importing subject, and vouching the named variable
// records the discharge on the fingerprint produced through the same
// policy-scoped engine the verdicts use
// (REQ-evidence-witness-freshness's vouch discipline).
//
//gofresh:pure
func TestPolicyVouchReachesTheCaptureEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds gofresh views over the protobuf graph")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "google.golang.org/protobuf").Output()
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(out))
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/vouchfix\n\ngo 1.26\n\nrequire google.golang.org/protobuf " + version + "\n",
		"reg/reg.go": `package reg

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func Count() int {
	n := 0
	protoregistry.GlobalFiles.RangeFiles(func(protoreflect.FileDescriptor) bool {
		n++
		return true
	})
	return n
}
`,
		"reg/reg_test.go": `package reg

import "testing"

func TestCount(t *testing.T) {
	if Count() < 0 {
		t.Fatal("count")
	}
}
`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	ctx := context.Background()
	var lastFingerprint gofresh.Fingerprint
	capture := func(raw string) (verdictReason, discharges string) {
		t.Helper()
		pol := &stipulatorv1.TestPolicy{}
		if err := prototext.Unmarshal([]byte(raw), pol); err != nil {
			t.Fatal(err)
		}
		pc, err := capturePolicy(ctx, dir, pol)
		if err != nil {
			t.Fatal(err)
		}
		if len(pc.groups) != 1 {
			t.Fatalf("groups = %d, want 1", len(pc.groups))
		}
		g := pc.groups[0]
		subjects := groupSubjects(g, pc.globalCount)
		if len(subjects) == 0 {
			t.Fatal("no witness subjects")
		}
		engine, err := groupEngine(ctx, dir, g)
		if err != nil {
			t.Fatal(err)
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := view.Capture(ctx, subjects[0])
		if err != nil {
			t.Fatal(err)
		}
		verdict, err := view.Check(ctx, fingerprint, subjects[0])
		if err != nil {
			t.Fatal(err)
		}
		lastFingerprint = fingerprint
		return verdict.Reason, fingerprint.DynamicStateVouches
	}
	checkUnder := func(raw string, fp gofresh.Fingerprint) string {
		t.Helper()
		pol := &stipulatorv1.TestPolicy{}
		if err := prototext.Unmarshal([]byte(raw), pol); err != nil {
			t.Fatal(err)
		}
		pc, err := capturePolicy(ctx, dir, pol)
		if err != nil {
			t.Fatal(err)
		}
		g := pc.groups[0]
		subjects := groupSubjects(g, pc.globalCount)
		engine, err := groupEngine(ctx, dir, g)
		if err != nil {
			t.Fatal(err)
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			t.Fatal(err)
		}
		verdict, err := view.Check(ctx, fp, subjects[0])
		if err != nil {
			t.Fatal(err)
		}
		return verdict.Reason
	}

	const base = `invocations {
  name: "race"
  timeout { seconds: 600 }
  go {
    packages: "./..."
    race: true
  }
}
`
	reason, discharges := capture(base)
	if !strings.Contains(reason, "shares mutated dynamic state") {
		t.Fatalf("protobuf-importing subject not downgraded: %q", reason)
	}
	if discharges != "" {
		t.Fatalf("unvouched capture recorded a discharge: %q", discharges)
	}
	m := regexp.MustCompile(`([^\s:]+): ([^\s:]+)\.([\p{L}_][\p{L}\p{Nd}_]*) `).FindStringSubmatch(reason + " ")
	if m == nil || m[1] != m[2] {
		t.Fatalf("no culprit parsed from %q", reason)
	}
	culprit := m[1] + "." + m[3]

	vouched := `invocations {
  name: "race"
  timeout { seconds: 600 }
  go {
    packages: "./..."
    race: true
    dynamic_state_vouches { package: "` + m[1] + `" variable: "` + m[3] + `" }
  }
}
`
	reason2, discharges2 := capture(vouched)
	if discharges2 != culprit {
		t.Fatalf("vouch did not reach the engine: discharges = %q, want %q (verdict %q)", discharges2, culprit, reason2)
	}
	if strings.Contains(reason2+" ", culprit+" ") {
		t.Fatalf("vouched culprit still named by the verdict: %q", reason2)
	}
	vouchedFingerprint := lastFingerprint

	// The withdrawn-vouch direction, serve-shaped: the vouched record's
	// fingerprint checked under an engine built from the base policy -
	// the current-policy engine the serve path uses - refuses, naming
	// the resurfaced culprit; nothing compares the recorded vouches.
	withdrawnReason := checkUnder(base, vouchedFingerprint)
	if !strings.Contains(withdrawnReason, culprit) || !strings.Contains(withdrawnReason, "shares mutated dynamic state") {
		t.Fatalf("withdrawn-vouch check = %q, want the resurfaced culprit named", withdrawnReason)
	}
}
