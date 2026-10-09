package golang

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/stipulator/stipulate"
)

// siteAfter is the tree-relative site of the first fixture line holding
// text at or after the line holding anchor — the spelling a chain link
// carries.
func siteAfter(t *testing.T, rel, anchor, text string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata/fixturemod", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	from := 0
	for i, line := range lines {
		if strings.Contains(line, anchor) {
			from = i
			break
		}
	}
	for i := from; i < len(lines); i++ {
		if strings.Contains(lines[i], text) {
			return fmt.Sprintf("%s:%d", rel, i+1)
		}
	}
	t.Fatalf("%s: no %q after %q", rel, text, anchor)
	return ""
}

// TestExplainWitnessRendersServingsWalk pins the seeding chain: the
// witness at its declaration, then — for a direct driver — the driving
// call; for a helper-indirected driver, every hop of serving's walk as
// a call link naming the callee at the call's site and the driving
// call in the last helper's body; for a refused walk, the hops to the
// refusing call and the refusal at its site; and for a body nothing
// seeds, the witness alone under its class (REQ-mcp-explain).
func TestExplainWitnessRendersServingsWalk(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the fixture backend the full tier loads")
	}
	stipulate.Covers(t, "REQ-mcp-explain")
	fb := fixtureBackend(t)
	const lib = "example.com/fixture/lib"
	const bad = "example.com/fixture/badhelper"
	badFile := "badhelper/bad.go"
	for _, tc := range []struct {
		symbol string
		want   gofresh.Chain
	}{
		{lib + ".TestPropRapidCheck", gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropRapidCheck", Pos: siteAfter(t, "lib/prop_test.go", "", "func TestPropRapidCheck(")},
			{Kind: LinkDriver, Package: lib, Symbol: "TestPropRapidCheck", Clause: driverClause, Pos: siteAfter(t, "lib/prop_test.go", "func TestPropRapidCheck(", "rapid.Check(t, func(")},
		}}},
		{lib + ".TestPropViaTwoHops", gofresh.Chain{Arm: ArmSeedingThrough, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaTwoHops", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaTwoHops(")},
			{Kind: LinkCall, Package: lib, Symbol: "TestPropViaTwoHops", Callee: lib + ".runPropTwice", Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaTwoHops(", "runPropTwice(t, func(")},
			{Kind: LinkCall, Package: lib, Symbol: "runPropTwice", Callee: lib + ".runProp", Pos: siteAfter(t, "lib/propvia_test.go", "func runPropTwice(", "runProp(t, body)")},
			{Kind: LinkDriver, Package: lib, Symbol: "runProp", Clause: driverClause, Pos: siteAfter(t, "lib/propvia_test.go", "func runProp(", "rapid.Check(t, body)")},
		}}},
		{lib + ".TestPropViaMethod", gofresh.Chain{Arm: ArmSeedingThrough, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaMethod", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaMethod(")},
			{Kind: LinkCall, Package: lib, Symbol: "TestPropViaMethod", Callee: "(" + lib + ".propRunner).Run", Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaMethod(", "propRunner{}.Run(t, func(")},
			{Kind: LinkDriver, Package: lib, Symbol: "(propRunner).Run", Clause: driverClause, Pos: siteAfter(t, "lib/propvia_test.go", "func (propRunner) Run(", "rapid.Check(t, body)")},
		}}},
		{lib + ".TestPropViaBadHelper", gofresh.Chain{Arm: ArmSeedingRefused, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaBadHelper", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaBadHelper(")},
			{Kind: LinkCall, Package: lib, Symbol: "TestPropViaBadHelper", Callee: bad + ".Run", Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaBadHelper(", "badhelper.Run(t, func(")},
			{Kind: LinkRefusal, Package: bad, Symbol: "Run", Clause: seededRefusal(errors.New("call of mystery in " + bad + ".Run resolves to no declaration")).String(), Pos: siteAfter(t, badFile, "func Run(", "mystery(t)")},
		}}},
		// A refusal met before the driving hop: the chain is the hop's.
		{lib + ".TestPropViaBadThenGood", gofresh.Chain{Arm: ArmSeedingThrough, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaBadThenGood", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaBadThenGood(")},
			{Kind: LinkCall, Package: lib, Symbol: "TestPropViaBadThenGood", Callee: lib + ".runProp", Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaBadThenGood(", "runProp(t, func(")},
			{Kind: LinkDriver, Package: lib, Symbol: "runProp", Clause: driverClause, Pos: siteAfter(t, "lib/propvia_test.go", "func runProp(", "rapid.Check(t, body)")},
		}}},
		// Two refusals: the first in breadth-first order is the chain's.
		{lib + ".TestPropViaTwoBad", gofresh.Chain{Arm: ArmSeedingRefused, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaTwoBad", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaTwoBad(")},
			{Kind: LinkCall, Package: lib, Symbol: "TestPropViaTwoBad", Callee: bad + ".Run", Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaTwoBad(", "badhelper.Run(t, func(")},
			{Kind: LinkRefusal, Package: bad, Symbol: "Run", Clause: seededRefusal(errors.New("call of mystery in " + bad + ".Run resolves to no declaration")).String(), Pos: siteAfter(t, badFile, "func Run(", "mystery(t)")},
		}}},
		// Two direct drivers: the first call is the driving site.
		{lib + ".TestPropTwoDrivers", gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropTwoDrivers", Pos: siteAfter(t, "lib/prop_test.go", "", "func TestPropTwoDrivers(")},
			{Kind: LinkDriver, Package: lib, Symbol: "TestPropTwoDrivers", Clause: driverClause, Pos: siteAfter(t, "lib/prop_test.go", "func TestPropTwoDrivers(", "rapid.Check(t, func(")},
		}}},
		// A function named as a value: the call link sits at the name.
		{lib + ".TestPropViaFuncValue", gofresh.Chain{Arm: ArmSeedingThrough, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaFuncValue", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaFuncValue(")},
			{Kind: LinkCall, Package: lib, Symbol: "TestPropViaFuncValue", Callee: lib + ".runPropSub", Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaFuncValue(", "t.Run(\"sub\", runPropSub)")},
			{Kind: LinkDriver, Package: lib, Symbol: "runPropSub", Clause: driverClause, Pos: siteAfter(t, "lib/propvia_test.go", "func runPropSub(", "rapid.Check(t, func(")},
		}}},
		// The driver itself named as a value: direct, at the name.
		{lib + ".TestPropViaDriverValue", gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaDriverValue", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaDriverValue(")},
			{Kind: LinkDriver, Package: lib, Symbol: "TestPropViaDriverValue", Clause: driverClause, Pos: siteAfter(t, "lib/propvia_test.go", "func TestPropViaDriverValue(", "check := rapid.Check")},
		}}},
		// A method value off an interface-typed value is a dispatch:
		// outside the walk, as the called form.
		{lib + ".TestPropViaInterfaceMethodValue", gofresh.Chain{Arm: ArmNotSeeded + " (example)", Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaInterfaceMethodValue", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaInterfaceMethodValue(")},
		}}},
		// A driver called through a dot import: direct, at the bare name.
		{lib + ".TestPropDotImported", gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropDotImported", Pos: siteAfter(t, "lib/dotprop_test.go", "", "func TestPropDotImported(")},
			{Kind: LinkDriver, Package: lib, Symbol: "TestPropDotImported", Clause: driverClause, Pos: siteAfter(t, "lib/dotprop_test.go", "func TestPropDotImported(", "Check(t, func(")},
		}}},
		// A value from elsewhere: outside the walk.
		{lib + ".TestPropViaValueFromElsewhere", gofresh.Chain{Arm: ArmNotSeeded + " (example)", Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropViaValueFromElsewhere", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPropViaValueFromElsewhere(")},
		}}},
		{lib + ".TestPropQuickCheck", gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPropQuickCheck", Pos: siteAfter(t, "lib/prop_test.go", "", "func TestPropQuickCheck(")},
			{Kind: LinkDriver, Package: lib, Symbol: "TestPropQuickCheck", Clause: driverClause, Pos: siteAfter(t, "lib/prop_test.go", "func TestPropQuickCheck(", "quick.Check(")},
		}}},
		{lib + ".TestPlainViaHelper", gofresh.Chain{Arm: ArmNotSeeded + " (example)", Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestPlainViaHelper", Pos: siteAfter(t, "lib/propvia_test.go", "", "func TestPlainViaHelper(")},
		}}},
		{lib + ".TestProofThenDrive", gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "TestProofThenDrive", Pos: siteAfter(t, "lib/proofdrive_test.go", "", "func TestProofThenDrive(")},
			{Kind: LinkDriver, Package: lib, Symbol: "TestProofThenDrive", Clause: driverClause, Pos: siteAfter(t, "lib/proofdrive_test.go", "func TestProofThenDrive(", "rapid.Check(")},
		}}},
		{lib + ".Add", gofresh.Chain{Arm: ArmNotSeeded + " (not a runnable test witness)", Links: []gofresh.ChainLink{
			{Kind: LinkWitness, Package: lib, Symbol: "Add", Pos: siteAfter(t, "lib/lib.go", "", "func Add(")},
		}}},
	} {
		chain, view, err := fb.explainWitness(tc.symbol)
		if err != nil {
			t.Fatalf("%s: %v", tc.symbol, err)
		}
		if view != "default" {
			t.Fatalf("%s: view %q, want the resolved view", tc.symbol, view)
		}
		if !reflect.DeepEqual(chain, tc.want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", tc.symbol, chain, tc.want)
		}
	}
	if _, _, err := fb.explainWitness(lib + ".NoSuch"); err == nil || !strings.Contains(err.Error(), "witness "+lib+".NoSuch") {
		t.Fatalf("an unresolvable witness: %v, want the load gap named", err)
	}
	// A callee whose declaration cannot be read: the refusal link is
	// the caller's call of it, at that call's site, with no hop past
	// it — witnessed over a scoped backend whose on-demand load points
	// at an empty directory.
	scoped, err := newContext(context.Background(), "testdata/fixturemod", []string{lib})
	if err != nil {
		t.Fatal(err)
	}
	for _, cfgs := range scoped.lazyCfg {
		for _, cfg := range cfgs {
			cfg.Dir = t.TempDir()
		}
	}
	chain, view, err := scoped.explainWitness(lib + ".TestPropViaOtherPackage")
	if err != nil || view != "default" || chain.Arm != ArmSeedingRefused || len(chain.Links) != 2 {
		t.Fatalf("unreadable declaration: %v view %q\n%+v", err, view, chain)
	}
	at := chain.Links[1]
	if at.Kind != LinkRefusal || at.Package != lib || at.Symbol != "TestPropViaOtherPackage" || at.Pos != siteAfter(t, "lib/propvia_test.go", "func TestPropViaOtherPackage(", "helpers.Run(") || !strings.HasPrefix(at.Clause, reasonSeedingRefused.prefix()) || !strings.Contains(at.Clause, "example.com/fixture/helpers") {
		t.Fatalf("unreadable declaration's refusal link: %+v", at)
	}
	// Reached through a hop: the refusing body is the helper whose
	// call cannot be read, after the hop into it.
	chain, _, err = scoped.explainWitness(lib + ".TestPropViaOtherThroughHop")
	if err != nil || chain.Arm != ArmSeedingRefused || len(chain.Links) != 3 {
		t.Fatalf("unreadable declaration through a hop: %v\n%+v", err, chain)
	}
	hop, at := chain.Links[1], chain.Links[2]
	if hop.Kind != LinkCall || hop.Symbol != "TestPropViaOtherThroughHop" || hop.Callee != lib+".viaHelpers" || at.Kind != LinkRefusal || at.Package != lib || at.Symbol != "viaHelpers" || at.Pos != siteAfter(t, "lib/propvia_test.go", "func viaHelpers(", "helpers.Run(") || !strings.Contains(at.Clause, "example.com/fixture/helpers") {
		t.Fatalf("unreadable declaration through a hop: %+v %+v", hop, at)
	}
}

// TestExplainWitnessAnswersAcrossViews pins the other-view arms over a
// tag-split module, in process and through the whole-tree form's
// child: a direct driver in the tagged view's body answers direct
// under that view; a helper driving only in the tagged view answers
// through helpers under that view with the hops at their sites
// (REQ-mcp-explain across REQ-go-build-selections).
func TestExplainWitnessAnswersAcrossViews(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture module under two views")
	}
	stipulate.Covers(t, "REQ-mcp-explain")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	neutralAmbient(t)
	dir := splitModule(t)
	dstPolicy(t, dir)
	both, err := newContext(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	const lib = "example.com/split/lib"
	wantDirect := gofresh.Chain{Arm: ArmSeedingDirect, Links: []gofresh.ChainLink{
		{Kind: LinkWitness, Package: lib, Symbol: "TestSplitDirect", Pos: "lib/direct_dst_test.go:11"},
		{Kind: LinkDriver, Package: lib, Symbol: "TestSplitDirect", Clause: driverClause, Pos: "lib/direct_dst_test.go:12"},
	}}
	wantThrough := gofresh.Chain{Arm: ArmSeedingThrough, Links: []gofresh.ChainLink{
		{Kind: LinkWitness, Package: lib, Symbol: "TestSplit", Pos: "lib/split_test.go:9"},
		{Kind: LinkCall, Package: lib, Symbol: "TestSplit", Callee: lib + ".splitDrive", Pos: "lib/split_test.go:10"},
		{Kind: LinkDriver, Package: lib, Symbol: "splitDrive", Clause: driverClause, Pos: "lib/dst.go:12"},
	}}
	// A witness nothing seeds in either view answers under the
	// resolved view at its own declaration: another view's answer is
	// adopted only with a refusal or a hop.
	wantPlain := gofresh.Chain{Arm: ArmNotSeeded + " (example)", Links: []gofresh.ChainLink{
		{Kind: LinkWitness, Package: lib, Symbol: "TestSplitPlain", Pos: "lib/plain_default_test.go:7"},
	}}
	whole, err := NewWholeTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer whole.Close()
	for _, face := range []struct {
		name string
		ask  func(string) (gofresh.Chain, string, error)
	}{{"in process", both.explainWitness}, {"through the child", whole.ExplainWitness}} {
		for _, tc := range []struct {
			symbol string
			view   string
			want   gofresh.Chain
		}{{lib + ".TestSplitDirect", "-tags=dst", wantDirect}, {lib + ".TestSplit", "-tags=dst", wantThrough}, {lib + ".TestSplitPlain", "default", wantPlain}} {
			chain, view, err := face.ask(tc.symbol)
			if err != nil {
				t.Fatalf("%s %s: %v", face.name, tc.symbol, err)
			}
			if view != tc.view || !reflect.DeepEqual(chain, tc.want) {
				t.Fatalf("%s %s: view %q\n got %+v\nwant %+v", face.name, tc.symbol, view, chain, tc.want)
			}
		}
	}
	if _, _, err := whole.ExplainWitness(lib + ".NoSuch"); err == nil || !strings.Contains(err.Error(), "witness "+lib+".NoSuch") {
		t.Fatalf("the child's refusal: %v, want the load gap named", err)
	}
	// A refusal raised in another view answers under that view: the
	// dst helper calls an undeclared function while the default body
	// is clean.
	files := splitModuleFiles()
	files["lib/dst.go"] = "//go:build dst\n\npackage lib\n\nimport (\n\t\"testing\"\n\n\t\"pgregory.net/rapid\"\n)\n\nfunc splitDrive(t *testing.T, body func(*rapid.T)) {\n\tmystery(t, body)\n}\n"
	rdir := writeModule(t, files)
	dstPolicy(t, rdir)
	refusing, err := newContext(context.Background(), rdir, nil)
	if err != nil {
		t.Fatal(err)
	}
	refusal := seededRefusal(errors.New("call of mystery in " + lib + ".splitDrive resolves to no declaration")).String()
	served, err := refusing.NeverServe([]string{lib + ".TestSplit"})
	if err != nil || served[lib+".TestSplit"] != refusal {
		t.Fatalf("the dst view's refusal: %v %v", served, err)
	}
	chain, view, err := refusing.explainWitness(lib + ".TestSplit")
	wantRefused := gofresh.Chain{Arm: ArmSeedingRefused, Links: []gofresh.ChainLink{
		{Kind: LinkWitness, Package: lib, Symbol: "TestSplit", Pos: "lib/split_test.go:9"},
		{Kind: LinkCall, Package: lib, Symbol: "TestSplit", Callee: lib + ".splitDrive", Pos: "lib/split_test.go:10"},
		{Kind: LinkRefusal, Package: lib, Symbol: "splitDrive", Clause: refusal, Pos: "lib/dst.go:12"},
	}}
	if err != nil || view != "-tags=dst" || !reflect.DeepEqual(chain, wantRefused) {
		t.Fatalf("a refusal in the dst view: %v view %q\n got %+v\nwant %+v", err, view, chain, wantRefused)
	}
}

// TestSeedingChainIsBoundedKeepingTheDecidingLink pins the cap: a
// chain past the bound keeps its first links and its last — the
// deciding one — and counts the rest (REQ-mcp-explain).
//
//gofresh:pure
func TestSeedingChainIsBoundedKeepingTheDecidingLink(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain")
	var links []gofresh.ChainLink
	for i := 0; i < witnessChainBound+5; i++ {
		links = append(links, gofresh.ChainLink{Kind: LinkCall, Symbol: strconv.Itoa(i)})
	}
	links[len(links)-1] = gofresh.ChainLink{Kind: LinkDriver, Symbol: "deciding"}
	c := boundChain(ArmSeedingThrough, links)
	if c.Omitted != 5 || len(c.Links) != witnessChainBound {
		t.Fatalf("bound: %d links, %d omitted", len(c.Links), c.Omitted)
	}
	if c.Links[0].Symbol != "0" || c.Links[witnessChainBound-2].Symbol != strconv.Itoa(witnessChainBound-2) || c.Links[witnessChainBound-1].Kind != LinkDriver {
		t.Fatalf("the kept links: first %+v, last %+v", c.Links[0], c.Links[witnessChainBound-1])
	}
	exact := boundChain(ArmSeedingDirect, links[:witnessChainBound])
	if exact.Omitted != 0 || len(exact.Links) != witnessChainBound {
		t.Fatalf("a chain at the bound: %d links, %d omitted", len(exact.Links), exact.Omitted)
	}
}

// TestReasonClassesCarryTheirExplainKind pins the table: every reason
// the backend mints classifies to the class that minted it, with its
// explain kind; a judgment reason classifies whole and not with a
// detail; a spelling no class owns is foreign (REQ-mcp-explain,
// REQ-evidence-witness-freshness-diagnosable).
//
//gofresh:pure
func TestReasonClassesCarryTheirExplainKind(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain", "REQ-evidence-witness-freshness-diagnosable")
	minted := map[string]explainKind{
		seededReason.String():                              explainWitness,
		seededThroughReason("example.com/p.run").String():  explainWitness,
		seededRefusal(errors.New("call of x")).String():    explainWitness,
		reasonStoreRefused.with("disk full").String():      explainSelf,
		reasonUnclassifiable.with("load errors").String():  explainSelf,
		reasonPostRun.with("reaches net").String():         explainCulprit,
		reasonObservationSeal.with("moved").String():       explainCulprit,
		reasonProofRefused.with("refused").String():        explainCulprit,
		reasonProducerFault.with("faulted").String():       explainSelf,
		reasonStateUnavailable.with("unreadable").String(): explainSelf,
		reasonSourceFailed.with("failed").String():         explainSelf,
		reasonDegraded.with("degraded").String():           explainSelf,
		reasonNotPublished.with("").String():               explainSelf,
		reasonNoCapture.with("no invocation").String():     explainSelf,
	}
	for _, r := range judgedReasons {
		minted[r.String()] = explainSelf
	}
	for reason, kind := range minted {
		c, ok := classifyReason(reason)
		if !ok || c.kind() != kind {
			t.Errorf("%q: class %+v ok=%v, want kind %d", reason, c, ok, kind)
		}
	}
	for _, foreign := range []string{"reaches testing.Run (test runtime execution)", reasonNoFingerprint.String() + ": detail", "", "post-run"} {
		if c, ok := classifyReason(foreign); ok {
			t.Errorf("%q classified as %+v, want foreign", foreign, c)
		}
	}
	if (uncacheable{}).String() != "" || reasonNone.kind() != 0 || reasonNone.prefix() != "" {
		t.Errorf("the zero reason renders %q with kind %d; want no reason — no prefix, no kind", (uncacheable{}).String(), reasonNone.kind())
	}
	if c, ok := classifyReason(""); ok || c != reasonNone {
		t.Errorf("an empty text classifies as %v ok=%v; want the none class, unknown", c, ok)
	}
	for _, c := range reasonClasses {
		if c.prefix() == "" || c.kind() == 0 {
			t.Errorf("class %+v: a prefix and a kind are required", c)
		}
		for _, d := range reasonClasses {
			if c != d && strings.HasPrefix(d.prefix(), c.prefix()) {
				t.Errorf("prefix %q begins %q: the table is not prefix-free", c.prefix(), d.prefix())
			}
		}
		for _, r := range judgedReasons {
			if strings.HasPrefix(r.String(), c.prefix()) {
				t.Errorf("prefix %q begins the judgment reason %q", c.prefix(), r)
			}
		}
	}
	if len(judgedReasons) != 6 {
		t.Fatalf("the judgment vocabulary has %d reasons, the table lists %d", 6, len(judgedReasons))
	}
}

// TestParseReasonAdmitsEveryClassAndWrapsTheForeign pins the text
// boundary: a reason of every class, composed over a detail (the
// judged vocabulary whole), crosses the text form and comes back as
// the same class and detail; a spelling no class owns is admitted
// fail-closed under the unclassifiable class with the spelling as its
// detail, so it never serves and explain answers it as its own
// attribution (REQ-mcp-explain, REQ-evidence-witness-freshness-diagnosable).
//
//gofresh:pure
func TestParseReasonAdmitsEveryClassAndWrapsTheForeign(t *testing.T) {
	stipulate.Covers(t, "REQ-mcp-explain", "REQ-evidence-witness-freshness-diagnosable")
	for _, c := range reasonClasses {
		for _, detail := range []string{"", "x", "post-run validation: nested", "a: b: c"} {
			minted := c.with(detail)
			if got := parseReason(minted.String()); got != minted {
				t.Errorf("%q: parsed as %+v, want %+v", minted.String(), got, minted)
			}
		}
	}
	for _, r := range judgedReasons {
		if got := parseReason(r.String()); got != r {
			t.Errorf("%q: parsed as %+v, want the judged reason whole", r.String(), got)
		}
	}
	for _, foreign := range []string{"reaches testing.Run (test runtime execution)", "", "post-run", reasonNoFingerprint.String() + ": d"} {
		got := parseReason(foreign)
		if got.class != reasonUnclassifiable || got.detail != foreignSpellingDetail+foreign {
			t.Errorf("%q: admitted as %+v, want the unclassifiable class over the spelling", foreign, got)
		}
		if c, ok := classifyReason(got.String()); !ok || c.kind() != explainSelf {
			t.Errorf("%q: the wrapped spelling classifies as %+v ok=%v, want its own attribution", foreign, c, ok)
		}
		if again := parseReason(got.String()); again != got {
			t.Errorf("%q: the wrapped spelling parsed again as %+v, want itself", foreign, again)
		}
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestMissingDeclarationRefusalNamesThePlacement pins the one fail-closed
// arm the walk keeps for a held package declaring no such function: the
// refusal names the function, the package, the view, and — when the type
// information places the function — its site; a function of a universe
// the construction does not hold is placed nowhere
// (REQ-evidence-witness-freshness-seeded).
func TestMissingDeclarationRefusalNamesThePlacement(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the fixture backend the full tier loads")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-seeded")
	fb := fixtureBackend(t)
	const lib = "example.com/fixture/lib"
	ghost := types.NewFunc(token.NoPos, types.NewPackage(lib, "lib"), "Ghost", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	fb.walkMu.Lock()
	fd, _, err := fb.funcDeclOf(SelectionKey(nil, ""), ghost)
	fb.walkMu.Unlock()
	want := "declaration of " + lib + ".Ghost is not among the parsed files of in-module package " + lib + " in the \"default\" view"
	if err == nil || fd != nil || err.Error() != want {
		t.Fatalf("a held package's ghost: fd=%v err=%v, want %q", fd, err, want)
	}
	// A real object is placed where the type information declares it.
	obj := fb.object(lib + ".Add")
	fn, ok := obj.(*types.Func)
	if !ok {
		t.Fatalf("Add's object: %T", obj)
	}
	if got, want := fb.placedAt(fn), " (the type information places it at "+siteAfter(t, "lib/lib.go", "", "func Add(")+")"; got != want {
		t.Fatalf("placedAt = %q, want %q", got, want)
	}
	for sel, label := range map[string]string{SelectionKey(nil, ""): "default", SelectionKey([]string{"dst", "race"}, ""): "-tags=dst,race", SelectionKey([]string{"dst"}, "go1.27.0"): "-tags=dst toolchain go1.27.0"} {
		if got := viewLabel(sel); got != label {
			t.Errorf("viewLabel(%q) = %q, want %q", sel, got, label)
		}
	}
}
