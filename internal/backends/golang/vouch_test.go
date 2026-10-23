package golang

import (
	"archive/zip"
	"context"
	"flag"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
)

// The policy's reviewed vouch set reaches the capture engine: a real
// version-pinned dependency culprit (protobuf's global registries)
// downgrades an importing subject, and vouching the named variable
// records the discharge on the fingerprint produced through the same
// policy-scoped engine the verdicts use
// (REQ-evidence-witness-freshness-vouches's vouch discipline).
//
//gofresh:pure
func TestPolicyVouchReachesTheCaptureEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds gofresh views over the protobuf graph")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-vouches")
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
		pc, err := mustCapture(t, ctx, dir, pol).discover(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(pc.groups) != 1 {
			t.Fatalf("groups = %d, want 1", len(pc.groups))
		}
		g := pc.groups[0]
		subjects := groupSubjects(g)
		if len(subjects) == 0 {
			t.Fatal("no witness subjects")
		}
		snapshot, _, err := groupSnapshot(ctx, dir, g)
		if err != nil {
			t.Fatal(err)
		}
		view, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: dir, Environment: g.witnessEnv})
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := view.CurrentFacts(ctx, subjects[0])
		if err != nil {
			t.Fatal(err)
		}
		verdict, err := view.Check(ctx, subjects[0], fingerprint)
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
		pc, err := mustCapture(t, ctx, dir, pol).discover(ctx)
		if err != nil {
			t.Fatal(err)
		}
		g := pc.groups[0]
		subjects := groupSubjects(g)
		snapshot, _, err := groupSnapshot(ctx, dir, g)
		if err != nil {
			t.Fatal(err)
		}
		view, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: dir, Environment: g.witnessEnv})
		if err != nil {
			t.Fatal(err)
		}
		verdict, err := view.Check(ctx, subjects[0], fp)
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

	// The repository's own vouch file is declined: a file entry naming
	// the culprit while the policy names no vouch neither discharges
	// nor licenses — the policy is the reviewed set's one home
	// (REQ-vouch-input's consumer rule).
	if err := os.WriteFile(filepath.Join(dir, "vouches"), []byte(m[1]+":"+m[3]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reasonFile, dischargesFile := capture(base)
	if dischargesFile != "" || !strings.Contains(reasonFile+" ", culprit+" ") {
		t.Fatalf("the repository vouch file licensed the engine: discharges %q, verdict %q", dischargesFile, reasonFile)
	}
	if err := os.Remove(filepath.Join(dir, "vouches")); err != nil {
		t.Fatal(err)
	}

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

var reviewedVouchRestart = flag.String("reviewed-vouch-restart", "", "internal reviewed-vouch restart test phase and tree")

// TestReviewedVouchRestartKeepsOnlyUsedAcceptances exercises publication and
// disk reload in separate processes. An unused policy row cannot revoke a
// record; the native used acceptance remains required after restart.
func TestReviewedVouchRestartKeepsOnlyUsedAcceptances(t *testing.T) {
	if *reviewedVouchRestart != "" {
		phase, dir, ok := strings.Cut(*reviewedVouchRestart, "\n")
		if !ok {
			t.Fatal("missing restart tree")
		}
		run, err := RunWitnesses(t.Context(), dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		switch phase {
		case "cold":
			if run.Ran != 1 || run.Fresh != 0 || run.Uncached != 0 {
				t.Fatalf("cold: %+v", run)
			}
		case "warm":
			if run.Ran != 0 || run.Fresh != 1 {
				t.Fatalf("unused withdrawal after process restart: %+v", run)
			}
		case "withdrawn":
			if run.Ran != 1 || run.Fresh != 0 {
				t.Fatalf("used withdrawal after process restart: %+v", run)
			}
			if why := run.ExecutedReasons["example.com/vouchrestart/reg.TestRegistry"]; !strings.Contains(why, "example.com/revieweddep.Used") {
				t.Fatalf("native withdrawal did not name used culprit: %q", why)
			}
		default:
			t.Fatalf("unknown restart phase %q", phase)
		}
		return
	}
	if testing.Short() {
		t.Skip("executes native producers and restarts their captures")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-vouches")
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// A real version-addressed dependency with two reviewed globals. The
	// unused mutator makes the stability assertion substantive without
	// importing unrelated I/O effects into the witness's native proof.
	const pkg = "example.com/revieweddep"
	const mod = "module " + pkg + "\n\ngo 1.26\n"
	proxy := t.TempDir()
	versionDir := filepath.Join(proxy, pkg, "@v")
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"v1.0.0.mod":  mod,
		"v1.0.0.info": `{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}`,
	} {
		if err := os.WriteFile(filepath.Join(versionDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(filepath.Join(versionDir, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, content := range map[string]string{
		"go.mod": mod,
		"dep.go": "package revieweddep\nvar Used = func() int { return 1 }\nvar Unused = 2\nfunc SetUsed(f func() int) { Used = f }\n",
	} {
		w, err := z.Create(pkg + "@v1.0.0/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	proxyURL := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(proxy), "/")}
	t.Setenv("GOPROXY", proxyURL.String())
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOMODCACHE", t.TempDir())
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/vouchrestart\n\ngo 1.26\n\nrequire " + pkg + " v1.0.0\n",
		"reg/reg_test.go": `package reg
import (
 "testing"
 "example.com/revieweddep"
)
func TestRegistry(t *testing.T) {
 if revieweddep.Used() != 1 { t.Fatal("wrong value") }
}
`,
	})
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod -modcacherw")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy: %v\n%s", err, out)
	}
	const used = pkg + ".Used"
	row := func(variable string) *stipulatorv1.DynamicStateVouch {
		v := &stipulatorv1.DynamicStateVouch{}
		v.SetPackage(pkg)
		v.SetVariable(variable)
		return v
	}
	cfg := &stipulatorv1.GoInvocationConfig{}
	cfg.SetPackages([]string{"./reg"})
	cfg.SetPlainWitness(true)
	cfg.SetDynamicStateVouches([]*stipulatorv1.DynamicStateVouch{row("Used"), row("Unused")})
	pol := &stipulatorv1.TestPolicy{}
	pol.SetInvocations([]*stipulatorv1.PolicyInvocation{goInvocation("vouches", cfg)})
	writePolicyRecord(t, dir, pol)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	restart := func(phase string) {
		t.Helper()
		// Phase travels in this harness's arguments, never the environment
		// whose complete compilation description must stay equal across runs.
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestReviewedVouchRestartKeepsOnlyUsedAcceptances$", "-reviewed-vouch-restart="+phase+"\n"+dir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s restart: %v\n%s", phase, err, output)
		}
	}
	restart("cold")
	records := witnesscache.Load(t.Context(), dir).Records
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	if records[0].Fingerprint.DynamicStateVouches != used {
		t.Fatalf("recorded full policy instead of used acceptances: %q", records[0].Fingerprint.DynamicStateVouches)
	}
	coordinate := records[0].IdentityKey()
	cfg.SetDynamicStateVouches([]*stipulatorv1.DynamicStateVouch{row("Used")})
	writePolicyRecord(t, dir, pol)
	restart("warm")
	reloaded := witnesscache.Load(t.Context(), dir).Records
	if len(reloaded) != 1 || reloaded[0].IdentityKey() != coordinate {
		t.Fatal("unused withdrawal re-addressed evidence")
	}
	cfg.SetDynamicStateVouches(nil)
	writePolicyRecord(t, dir, pol)
	restart("withdrawn")
}
