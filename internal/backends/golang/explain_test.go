package golang

import (
	"context"
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
		chain, v, err := ExplainDynamicState(context.Background(), tmp, pol, pkgPath, varName)
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
	arm, view, links, _ := explain("example.com/explainfix/both", "Registry")
	if arm != "" || view != "" || links != 0 {
		t.Fatalf("ambiguous package answered: arm=%q view=%q links=%d", arm, view, links)
	}
	arm, view, links, _ = explain("example.com/explainfix/reg", "Missing")
	if arm != "" || view != "" || links != 0 {
		t.Fatalf("non-culprit yielded a chain: arm=%q view=%q links=%d", arm, view, links)
	}
}
