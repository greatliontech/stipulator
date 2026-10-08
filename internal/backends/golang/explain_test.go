package golang

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
)

// The explain helper answers with the chain the freshness library
// derives, against the same policy-scoped views verdicts use: each
// culprit is answered by the view of the group whose invocation
// selects its package alone (named as the answering view), and a
// package two invocations select is in no view, so its culprit
// yields an empty chain even though a whole-tree glob would chain it.
//
//gofresh:pure
func TestExplainDynamicStateChainsThroughPolicyViews(t *testing.T) {
	if testing.Short() {
		t.Skip("measured heavy under the fast tier (in-process)")
	}
	stipulate.Covers(t, "REQ-mcp-explain")
	tmp := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/explainfix\n\ngo 1.26\n")
	refusingPkg := func(name string) string {
		return strings.ReplaceAll(`package NAME

type counter struct{ n int }

func (c *counter) Next(n int) int {
	c.n += n
	return c.n
}

type handler func(n int) int

func gen() map[string]handler {
	c := &counter{}
	return map[string]handler{"k": c.Next}
}

var Registry = gen()

func Count() int { return len(Registry) }
`, "NAME", name)
	}
	testFile := func(name string) string {
		return strings.ReplaceAll(`package NAME

import "testing"

func TestCount(t *testing.T) {
	if Count() != 1 {
		t.Fatal("count")
	}
}
`, "NAME", name)
	}
	for _, pkg := range []string{"reg", "other", "both", "lib", "p1", "p2"} {
		write(pkg+"/"+pkg+".go", refusingPkg(pkg))
		write(pkg+"/"+pkg+"_test.go", testFile(pkg))
	}
	// lib is a shared dependency of reg (invocation a) and other
	// (invocation b): its culprit chains in BOTH groups' views, so only
	// the deterministic group order decides which view answers.
	for _, pkg := range []string{"reg", "other"} {
		write(pkg+"/uses_lib.go", "package "+pkg+"\n\nimport \"example.com/explainfix/lib\"\n\nvar _ = lib.Count\n")
	}
	raw := `invocations {
  name: "a"
  timeout { seconds: 600 }
  go {
    packages: "./reg"
    packages: "./both"
    race: true
  }
}
invocations {
  name: "b"
  timeout { seconds: 600 }
  go {
    packages: "./other"
    packages: "./both"
    tags: "grpb"
    race: true
  }
}
invocations {
  name: "c"
  timeout { seconds: 600 }
  go {
    packages: "./p1"
    tags: "grpcd"
    race: true
  }
}
invocations {
  name: "d"
  timeout { seconds: 600 }
  go {
    packages: "./p2"
    tags: "grpcd"
    race: true
  }
}
`
	pol := &stipulatorv1.TestPolicy{}
	if err := prototext.Unmarshal([]byte(raw), pol); err != nil {
		t.Fatal(err)
	}
	explain := func(pkgPath, varName string) (arm, view string, links int, refusalPos string) {
		t.Helper()
		chain, v, err := ExplainDynamicState(context.Background(), mustCapture(t, context.Background(), tmp, pol), pkgPath, varName)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range chain.Links {
			if l.Kind == "refusal" && l.Symbol == "gen" && l.Clause != "" {
				refusalPos = l.Pos
			}
		}
		return chain.Arm, v, len(chain.Links), refusalPos
	}
	arm, view, _, pos := explain("example.com/explainfix/reg", "Registry")
	if arm != "environment-audit" || view != "a" || !strings.Contains(pos, "reg.go:") {
		t.Fatalf("reg: arm=%q view=%q refusal pos=%q", arm, view, pos)
	}
	arm, view, _, pos = explain("example.com/explainfix/other", "Registry")
	if arm != "environment-audit" || view != "b" || !strings.Contains(pos, "other.go:") {
		t.Fatalf("other: arm=%q view=%q refusal pos=%q", arm, view, pos)
	}
	arm, view, _, _ = explain("example.com/explainfix/lib", "Registry")
	if arm != "environment-audit" || view != "a" {
		t.Fatalf("contended culprit not answered by the first view in group order: arm=%q view=%q", arm, view)
	}
	arm, view, _, _ = explain("example.com/explainfix/p1", "Registry")
	if arm != "environment-audit" || view != "c,d" {
		t.Fatalf("multi-invocation group not named in full: arm=%q view=%q", arm, view)
	}
	// A package two groups select is covered by each; the first group in
	// deterministic key order answers, exactly as a shared dependency
	// does.
	arm, view, _, _ = explain("example.com/explainfix/both", "Registry")
	if arm != "environment-audit" || view != "a" {
		t.Fatalf("cross-group package not answered by the first covering view: arm=%q view=%q", arm, view)
	}
	arm, view, links, _ := explain("example.com/explainfix/reg", "Missing")
	if arm != "" || view != "" || links != 0 {
		t.Fatalf("non-culprit yielded a chain: arm=%q view=%q links=%d", arm, view, links)
	}
}

// Explain is the one entry both surfaces call: a policy fault carries
// the "explain: policy:" shape, and ResolveExplain's refusals spell the
// argument names the calling surface hands it (REQ-mcp-explain).
//
//gofresh:pure
func TestExplainEntryShapesItsRefusals(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain")
	if _, _, err := Explain(context.Background(), t.TempDir(), "example.com/p", "V"); err == nil || !strings.HasPrefix(err.Error(), "explain: policy: ") {
		t.Fatalf("policy fault shape: %v", err)
	}
	cli := func(name string) string { return "--" + name }
	mcp := func(n string) string { return n }
	for _, tc := range []struct {
		reason, pkg, sym, witness string
		spelling                  func(string) string
		want                      ExplainRequest
		refusal                   string
	}{
		{pkg: "example.com/p", spelling: cli, refusal: "explain: --package and --symbol travel together"},
		{spelling: mcp, refusal: "explain: pass reason to parse, witness for a witness's seeding, or package and symbol"},
		{reason: "nothing here", spelling: cli, refusal: "explain: no culprit parsed from the reason; pass --package and --symbol, or --witness for a seeding reason"},
		{reason: "github.com/x/reg: github.com/x/reg.Registry escapes writable", spelling: cli, want: ExplainRequest{Package: "github.com/x/reg", Symbol: "Registry"}},
		{reason: "ignored", pkg: "example.com/p", sym: "V", spelling: cli, want: ExplainRequest{Package: "example.com/p", Symbol: "V"}},
		// The witness form travels alone.
		{witness: "example.com/p.TestProp", spelling: cli, want: ExplainRequest{Witness: "example.com/p.TestProp"}},
		{witness: "example.com/p.TestProp", reason: seededReason.String(), spelling: cli, refusal: "explain: --witness travels alone — it names the witness whose seeding is derived"},
		{witness: "example.com/p.TestProp", pkg: "example.com/p", sym: "V", spelling: mcp, refusal: "explain: witness travels alone — it names the witness whose seeding is derived"},
		// Every seeding-family spelling names the witness form.
		{reason: seededReason.String(), spelling: cli, refusal: "explain: a seeding reason derives from the witness's own body; pass --witness naming the witness the reason stood beside"},
		{reason: seededThroughReason("example.com/p.run").String(), spelling: mcp, refusal: "explain: a seeding reason derives from the witness's own body; pass witness naming the witness the reason stood beside"},
		{reason: seededRefusal(errors.New("call of x in the bound body resolves to no declaration")).String(), spelling: cli, refusal: "explain: a seeding reason derives from the witness's own body; pass --witness naming the witness the reason stood beside"},
		// A freshness-library reason: the culprit from its tail, else
		// its own attribution.
		{reason: reasonPostRun.with("package graph shares mutated dynamic state: github.com/x/b: github.com/x/b.thresholds registers function values outside the environment-free audit").String(), spelling: cli, want: ExplainRequest{Package: "github.com/x/b", Symbol: "thresholds"}},
		{reason: reasonPostRun.with("reaches crypto/rand.Read (entropy)").String(), spelling: cli, want: ExplainRequest{Attribution: reasonPostRun.with("reaches crypto/rand.Read (entropy)").String()}},
		{reason: reasonObservationSeal.with("github.com/x/b: github.com/x/b.state escapes writable").String(), spelling: cli, want: ExplainRequest{Package: "github.com/x/b", Symbol: "state"}},
		// A reason that is its own attribution answers as such.
		{reason: reasonUnclassifiable.with("package example.com/p: load errors").String(), spelling: cli, want: ExplainRequest{Attribution: reasonUnclassifiable.with("package example.com/p: load errors").String()}},
		{reason: reasonNoFingerprint.String(), spelling: cli, want: ExplainRequest{Attribution: reasonNoFingerprint.String()}},
		{reason: reasonDegraded.with("x").String(), spelling: cli, want: ExplainRequest{Attribution: reasonDegraded.with("x").String()}},
		{reason: reasonStoreRefused.with("mkdir: permission denied").String(), spelling: mcp, want: ExplainRequest{Attribution: reasonStoreRefused.with("mkdir: permission denied").String()}},
		// The class decides, not the tail: a self reason carrying a
		// culprit-shaped detail is still its own attribution.
		{reason: reasonDegraded.with("github.com/x/b: github.com/x/b.state escapes writable").String(), spelling: cli, want: ExplainRequest{Attribution: reasonDegraded.with("github.com/x/b: github.com/x/b.state escapes writable").String()}},
	} {
		got, err := ResolveExplain(tc.reason, tc.pkg, tc.sym, tc.witness, tc.spelling)
		if tc.refusal != "" {
			if err == nil || err.Error() != tc.refusal {
				t.Errorf("%+v: err = %v, want %q", tc, err, tc.refusal)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%+v: = %+v, %v; want %+v", tc, got, err, tc.want)
		}
	}
}
