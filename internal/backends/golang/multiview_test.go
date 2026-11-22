package golang

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/greatliontech/gofresh"
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func publishResolution(t *testing.T, dir string, symbols ...string) {
	t.Helper()
	s, err := NewServed(t.Context(), dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, symbol := range symbols {
		if res, _, err := s.Resolve(symbol); err != nil || res != verify.Resolved {
			t.Fatalf("initial resolution (%s): %v, %v", symbol, res, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	warm, err := NewServed(t.Context(), dir, symbols)
	if err != nil {
		t.Fatal(err)
	}
	defer warm.Close()
	if warm.ServedCount() != len(symbols) {
		t.Fatalf("unchanged control did not serve: %v; %v", s.Notices(), warm.Notices())
	}
}

// A default-view answer includes the seeding walk of every selected view,
// even when that other view reaches a helper the default body never names.
func TestResolutionMultiViewCoversOtherViewHelpers(t *testing.T) {
	if testing.Short() {
		t.Skip("publishes and edits a helper in another selected view")
	}
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":          "module example.com/certificate\n\ngo 1.27\n",
		"p/p.go":          "package p\n",
		"p/plain_test.go": "//go:build !dst\n\npackage p\nimport \"testing\"\nfunc TestF(*testing.T) {}\n",
		"p/dst_test.go":   "//go:build dst\n\npackage p\nimport (\"testing\"; \"example.com/certificate/helper\")\nfunc TestF(t *testing.T) { helper.Run(t) }\n",
		"helper/plain.go": "//go:build !dst\n\npackage helper\nimport \"testing\"\nfunc Run(*testing.T) {}\n",
		"helper/dst.go":   "//go:build dst\n\npackage helper\nimport \"testing\"\nfunc Run(*testing.T) {}\n",
	})
	dstPolicy(t, dir)
	const symbol = "example.com/certificate/p.TestF"
	publishResolution(t, dir, symbol)
	if err := os.WriteFile(filepath.Join(dir, "helper", "dst.go"), []byte("//go:build dst\n\npackage helper\nimport (\"testing\"; \"testing/quick\")\nfunc Run(*testing.T) { _ = quick.Check(func(int) bool { return true }, nil) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := newContext(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := b.NeverServe([]string{symbol})
	if err != nil || !strings.Contains(want[symbol], "helper.Run") {
		t.Fatalf("fresh walk did not find helper: %v %v", want, err)
	}
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 0 {
		t.Errorf("single-view fingerprint served a cross-view answer")
	}
	got, err := s.NeverServe([]string{symbol})
	if err != nil || got[symbol] != want[symbol] {
		t.Fatalf("stale classification served: %v, want %v (%v)", got, want, err)
	}
}

// The absence of an earlier declaration is part of a later view's answer.
func TestResolutionMultiViewCoversEarlierDeclarationAbsence(t *testing.T) {
	if testing.Short() {
		t.Skip("publishes a tagged declaration then introduces an earlier one")
	}
	stipulate.Covers(t, "REQ-evidence-resolution-freshness", "REQ-go-build-selections")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.com/precedence\n\ngo 1.27\n",
		"plain.go": "//go:build !dst\n\npackage precedence\nfunc Anchor() {}\n",
		"dst.go":   "//go:build dst\n\npackage precedence\nfunc F(string) {}\n",
	})
	dstPolicy(t, dir)
	const symbol = "example.com/precedence.F"
	publishResolution(t, dir, symbol)
	if err := os.WriteFile(filepath.Join(dir, "plain.go"), []byte("//go:build !dst\n\npackage precedence\nfunc Anchor() {}\nfunc F(int) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := newContext(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, shape, key, err := b.ResolveIn(symbol)
	if err != nil || key != "default" {
		t.Fatalf("fresh default did not win: %q %v", key, err)
	}
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 0 {
		t.Errorf("earlier declaration was not a dependency")
	}
	if s.Reasons()[symbol] != "source dependencies" {
		t.Fatalf("earlier declaration reason = %q, want \"source dependencies\"", s.Reasons()[symbol])
	}
	res, got, gotKey, err := s.ResolveIn(symbol)
	if err != nil || res != verify.Resolved || got != shape || gotKey != "default" {
		t.Fatalf("served wrong declaration: %v %q %q %v, want %q in default", res, got, gotKey, err, shape)
	}
}

func TestResolutionMultiViewIsolatesPackageFailures(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-degrade")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":       "module example.com/certificate\n\ngo 1.27\n",
		"good/good.go": "package good\nfunc F() {}\n",
		"bad/bad.go":   "package bad\nfunc F() {}\nfunc G() {}\n",
	})
	const good = "example.com/certificate/good.F"
	const badF = "example.com/certificate/bad.F"
	const badG = "example.com/certificate/bad.G"
	publishResolution(t, dir, good, badF, badG)

	// Break package bad with a syntax error so gofresh's go list -deps fails on bad
	// while good remains loadable and servable.
	if err := os.WriteFile(filepath.Join(dir, "bad", "bad.go"), []byte("package bad\nfunc F( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldEngineHook := engineCommandHook
	t.Cleanup(func() { engineCommandHook = oldEngineHook })
	isolatedBadQueries := 0
	engineCommandHook = func(cmd *exec.Cmd) {
		if slices.Contains(cmd.Args, "-deps") && cmd.Args[len(cmd.Args)-1] == "example.com/certificate/bad" && !slices.Contains(cmd.Args, "example.com/certificate/good") {
			isolatedBadQueries++
		}
	}
	s, err := NewServed(t.Context(), dir, []string{good, badF, badG})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 1 || s.served[good].Symbol != good {
		t.Fatalf("independent package failure discarded good record: %v", s.Notices())
	}
	for _, name := range []string{"bad.F", "bad.G"} {
		if !strings.Contains(strings.Join(s.Notices(), "\n"), name) {
			t.Fatalf("package failure lost affected symbol %s: %v", name, s.Notices())
		}
	}
	if isolatedBadQueries != 1 {
		t.Fatalf("isolated failed package queried %d times during NewServed, want 1", isolatedBadQueries)
	}
	_ = s.Close()
	engineCommandHook = oldEngineHook

	// Cross-view isolation when the broken package is the sole package queried in
	// a secondary view while good's package is absent in that view.
	tagDir := writeModule(t, map[string]string{
		"go.mod":          "module example.com/tagiso\n\ngo 1.27\n",
		"good/good.go":    "//go:build !dst\n\npackage good\nfunc F() {}\n",
		"dstbad/dstbad.go": "//go:build dst\n\npackage dstbad\nfunc B() {}\n",
	})
	dstPolicy(t, tagDir)
	const tagGood = "example.com/tagiso/good.F"
	const tagBad = "example.com/tagiso/dstbad.B"
	publishResolution(t, tagDir, tagGood, tagBad)
	if err := os.WriteFile(filepath.Join(tagDir, "dstbad", "dstbad.go"), []byte("//go:build dst\n\npackage dstbad\nfunc B( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tagServed, err := NewServed(t.Context(), tagDir, []string{tagGood, tagBad})
	if err != nil {
		t.Fatal(err)
	}
	defer tagServed.Close()
	if tagServed.ServedCount() != 1 || tagServed.served[tagGood].Symbol != tagGood {
		t.Fatalf("single-package failure in secondary view discarded unrelated inventory-absent symbol: served=%d notices=%v", tagServed.ServedCount(), tagServed.Notices())
	}
}

func TestResolutionMultiViewBindsClassifierImage(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/classifier\n\ngo 1.27\n",
		"p.go":   "package classifier\nfunc F() {}\n",
	})
	const symbol = "example.com/classifier.F"
	publishResolution(t, dir, symbol)
	old := sourceClassifierIdentity
	t.Cleanup(func() { sourceClassifierIdentity = old })
	sourceClassifierIdentity = strings.Repeat("0", 64)
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 0 || s.Reasons()[symbol] != "build configuration" {
		t.Fatalf("another classifier image served or misreported reason: served=%d reasons=%v", s.ServedCount(), s.Reasons())
	}
	if res, _, err := s.Resolve(symbol); err != nil || res != verify.Resolved {
		t.Fatalf("classifier change did not fall back to derivation: %v %v", res, err)
	}
	_ = s.Close()

	// Unavailable classifier identity permits typed resolution while refusing both
	// record serving and publication.
	unavailDir := writeModule(t, map[string]string{
		"go.mod": "module example.com/classifierunavail\n\ngo 1.27\n",
		"p.go":   "package classifierunavail\nfunc F() {}\n",
	})
	const unavailSym = "example.com/classifierunavail.F"
	sourceClassifierIdentity = old
	publishResolution(t, unavailDir, unavailSym)
	oldErr := sourceClassifierError
	t.Cleanup(func() { sourceClassifierError = oldErr })
	sourceClassifierIdentity = ""
	sourceClassifierError = errors.New("executable unreadable")
	unavail, err := NewServed(t.Context(), unavailDir, []string{unavailSym})
	if err != nil {
		t.Fatal(err)
	}
	if unavail.ServedCount() != 0 {
		t.Fatalf("unavailable classifier identity served cached record: %v", unavail.Notices())
	}
	if res, _, err := unavail.Resolve(unavailSym); err != nil || res != verify.Resolved {
		t.Fatalf("unavailable classifier identity blocked typed resolution: %v %v", res, err)
	}
	if err := unavail.Close(); err != nil {
		t.Fatal(err)
	}
	freshDir := writeModule(t, map[string]string{
		"go.mod": "module example.com/classifiernopub\n\ngo 1.27\n",
		"p.go":   "package classifiernopub\nfunc F() {}\n",
	})
	const noPubSym = "example.com/classifiernopub.F"
	noPub, err := NewServed(t.Context(), freshDir, []string{noPubSym})
	if err != nil {
		t.Fatal(err)
	}
	if res, _, err := noPub.Resolve(noPubSym); err != nil || res != verify.Resolved {
		t.Fatalf("typed resolution failed under unavailable classifier: %v %v", res, err)
	}
	if err := noPub.Close(); err != nil {
		t.Fatal(err)
	}
	if got := resolutioncache.Load(freshDir); len(got) != 0 {
		t.Fatalf("published record under unavailable classifier identity: %+v", got)
	}
	noPubNotices := strings.Join(noPub.Notices(), "\n")
	if !strings.Contains(noPubNotices, "1 without an opening capture: "+noPubSym) {
		t.Fatalf("unavailable classifier publish account = %q, want unopened notice", noPubNotices)
	}
}

func TestResolutionMultiViewClosesOtherViewDependencies(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-typed-load")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.com/closing\n\ngo 1.27\n",
		"plain.go": "//go:build !dst\n\npackage closing\nfunc F() {}\n",
		"dst.go":   "//go:build dst\n\npackage closing\nfunc F() {}\n",
	})
	dstPolicy(t, dir)
	const symbol = "example.com/closing.F"
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if res, _, err := s.Resolve(symbol); err != nil || res != verify.Resolved {
		t.Fatalf("resolve: %v %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dst.go"), []byte("//go:build dst\n\npackage closing\nfunc F(int) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if recs := resolutioncache.Load(dir); len(recs) != 0 {
		t.Fatalf("published over a moved non-resolving view: %+v", recs)
	}
	if !strings.Contains(strings.Join(s.Notices(), "\n"), "moved") {
		t.Fatalf("closing refusal missing: %v", s.Notices())
	}
}

func TestResolutionMultiViewCoversViewPrecedence(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness", "REQ-go-build-selections")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":    "module example.com/order\n\ngo 1.27\n",
		"plain.go":  "//go:build !dst && !second\n\npackage order\nfunc Anchor() {}\n",
		"dst.go":    "//go:build dst\n\npackage order\nfunc F(string) {}\n",
		"second.go": "//go:build second\n\npackage order\nfunc F(int) {}\n",
	})
	policy := func(tags ...string) {
		t.Helper()
		p := &stipulatorv1.TestPolicy{}
		var invocations []*stipulatorv1.PolicyInvocation
		for i, tag := range tags {
			cfg := &stipulatorv1.GoInvocationConfig{}
			cfg.SetPackages([]string{"./..."})
			cfg.SetTags([]string{tag})
			invocations = append(invocations, goInvocation(string(rune('a'+i)), cfg))
		}
		p.SetInvocations(invocations)
		writePolicyRecord(t, dir, p)
	}
	policy("dst", "second")
	const symbol = "example.com/order.F"
	publishResolution(t, dir, symbol)
	policy("second", "dst")
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 0 {
		t.Fatal("a reordered declaration precedence reused the old answer")
	}
	_, _, key, err := s.ResolveIn(symbol)
	if err != nil || !strings.HasPrefix(key, "second\x00") {
		t.Fatalf("first declaring view did not change: %q %v", key, err)
	}
}

func TestResolutionMultiViewRequiresEveryView(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-degrade")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.com/incomplete\n\ngo 1.27\n",
		"plain.go": "//go:build !dst\n\npackage incomplete\nfunc F() {}\n",
		"dst.go":   "//go:build dst\n\npackage incomplete\nfunc F() { missing() }\n",
	})
	dstPolicy(t, dir)
	const symbol = "example.com/incomplete.F"
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if res, _, err := s.Resolve(symbol); err != nil || res != verify.Resolved {
		t.Fatalf("default resolution failed on broken non-default view: %v %v", res, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if recs := resolutioncache.Load(dir); len(recs) != 0 {
		t.Fatalf("a complete default view published a record despite a broken secondary view: %+v", recs)
	}
	if !strings.Contains(strings.Join(s.Notices(), "\n"), "missing") {
		t.Fatalf("degraded secondary view fault missing from notices: %v", s.Notices())
	}
}

func TestResolutionMultiViewClosesBeforeServing(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/serveclose\n\ngo 1.27\n",
		"p/p.go":           "package p\n",
		"p/plain_test.go":  "//go:build !dst\n\npackage p\nimport \"testing\"\nfunc TestF(*testing.T) {}\n",
		"p/dst_test.go":    "//go:build dst\n\npackage p\nimport (\"testing\"; \"example.com/serveclose/helper\")\nfunc TestF(t *testing.T) { helper.Run(t) }\n",
		"helper/helper.go": "package helper\nimport \"testing\"\nfunc Run(*testing.T) {}\n",
	})
	dstPolicy(t, dir)
	const symbol = "example.com/serveclose/p.TestF"
	publishResolution(t, dir, symbol)
	old := beforeServingCloseForTest
	t.Cleanup(func() { beforeServingCloseForTest = old })
	changed := false
	beforeServingCloseForTest = func() {
		changed = true
		if err := os.WriteFile(filepath.Join(dir, "helper", "helper.go"), []byte("package helper\nimport (\"testing\"; \"testing/quick\")\nfunc Run(*testing.T) { _ = quick.Check(func(int) bool { return true }, nil) }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !changed || s.ServedCount() != 0 {
		t.Fatalf("served across a dependency edit: changed=%t served=%d", changed, s.ServedCount())
	}
	got, err := s.NeverServe([]string{symbol})
	if err != nil || !strings.Contains(got[symbol], "helper.Run") {
		t.Fatalf("did not rederive the closing dependency: %v %v", got, err)
	}
	_ = s.Close()
	beforeServingCloseForTest = old

	// Mid-serving declaration deletion (SelectedSource UnknownSubjectsError) must
	// classify as "source dependencies moved during serving", not a degraded fault.
	delDir := writeModule(t, map[string]string{
		"go.mod": "module example.com/delclose\n\ngo 1.27\n",
		"p.go":   "package delclose\nfunc Keep() {}\nfunc Drop() {}\n",
	})
	const dropSym = "example.com/delclose.Drop"
	publishResolution(t, delDir, dropSym)
	beforeServingCloseForTest = func() {
		if err := os.WriteFile(filepath.Join(delDir, "p.go"), []byte("package delclose\nfunc Keep() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	delServed, err := NewServed(t.Context(), delDir, []string{dropSym})
	if err != nil {
		t.Fatal(err)
	}
	if delServed.ServedCount() != 0 || delServed.Reasons()[dropSym] != "source dependencies moved during serving" || len(delServed.Degraded()) != 0 {
		t.Fatalf("mid-serving deletion misclassified: served=%d reasons=%v degraded=%v", delServed.ServedCount(), delServed.Reasons(), delServed.Degraded())
	}
	_ = delServed.Close()
	beforeServingCloseForTest = old

	// Mid-serving introduction of a dotted subpackage must refresh s.inventory on
	// fallback typed loading so s.Resolve resolves the new subpackage's symbol
	// rather than the stale receiver method, and publishes the new record cleanly.
	dotDir := writeModule(t, map[string]string{
		"go.mod": "module example.com/dottedclose\n\ngo 1.27\n",
		"p/p.go": "package p\ntype v2 struct{}\nfunc (v2) F() int { return 1 }\n",
	})
	const dotSym = "example.com/dottedclose/p.v2.F"
	publishResolution(t, dotDir, dotSym)
	beforeServingCloseForTest = func() {
		if err := os.MkdirAll(filepath.Join(dotDir, "p.v2"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dotDir, "p.v2", "new.go"), []byte("package pv2\nfunc F() string { return \"v2\" }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dotServed, err := NewServed(t.Context(), dotDir, []string{dotSym})
	if err != nil {
		t.Fatal(err)
	}
	beforeServingCloseForTest = old
	if dotServed.ServedCount() != 0 || dotServed.Reasons()[dotSym] != "source dependencies moved during serving" {
		t.Fatalf("mid-serving dotted subpackage escaped closing check: served=%d reasons=%v", dotServed.ServedCount(), dotServed.Reasons())
	}
	if pkg, err := dotServed.SymbolPackage(dotSym); err != nil || pkg != "example.com/dottedclose/p.v2" {
		t.Fatalf("fallback typed load kept stale package inventory: pkg=%q err=%v", pkg, err)
	}
	if res, _, err := dotServed.Resolve(dotSym); err != nil || res != verify.Resolved {
		t.Fatalf("fallback Resolve(%s): %v %v", dotSym, res, err)
	}
	if err := dotServed.Close(); err != nil {
		t.Fatal(err)
	}
	var foundNewPkg bool
	for _, rec := range resolutioncache.Load(dotDir) {
		if rec.Symbol == dotSym && rec.Package == "example.com/dottedclose/p.v2" {
			foundNewPkg = true
		}
	}
	if !foundNewPkg {
		t.Fatalf("republished record did not bind new dotted package: %+v; notices=%v", resolutioncache.Load(dotDir), dotServed.Notices())
	}
}

func TestResolutionMultiViewClosesGOENV(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/envclose\n\ngo 1.27\n",
		"p.go":   "package envclose\nfunc F() {}\n",
	})
	goenv := filepath.Join(t.TempDir(), "env")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(goenv, []byte("GOPROXY="+value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("https://proxy.golang.org,direct")
	t.Setenv("GOENV", goenv)
	t.Setenv("GOPROXY", "")
	const symbol = "example.com/envclose.F"
	publishResolution(t, dir, symbol)
	old := beforeServingCloseForTest
	t.Cleanup(func() { beforeServingCloseForTest = old })
	beforeServingCloseForTest = func() { write("off") }
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 0 || s.Reasons()[symbol] != "source dependencies moved during serving" {
		t.Fatalf("GOENV change escaped closing re-observation: %v", s.Notices())
	}
}

func TestResolutionMultiViewReopensAfterQuiesce(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-typed-load", "REQ-evidence-resolution-freshness-quiesce")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":   "module example.com/reopen\n\ngo 1.27\n",
		"plain.go": "//go:build !dst\n\npackage reopen\nfunc F() {}\nfunc G() {}\n",
		"dst.go":   "//go:build dst\n\npackage reopen\nfunc F() {}\nfunc G() {}\n",
	})
	dstPolicy(t, dir)
	const first, second = "example.com/reopen.F", "example.com/reopen.G"
	s, err := NewServed(t.Context(), dir, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ask(t, s, first)
	dstKey := s.sels[1].key()
	initialSecond := s.openingViews[dstKey].present[gofresh.Subject{Package: "example.com/reopen", Symbol: "G"}]
	if initialSecond.MaximalClosure == "" {
		t.Fatal("first opening did not observe the second subject")
	}
	s.Quiesce()
	initial := resolutioncache.Load(dir)
	if len(initial) != 1 || initial[0].Symbol != first {
		t.Fatalf("first opening did not publish: %v; %v", initial, s.Notices())
	}
	if err := os.WriteFile(filepath.Join(dir, "dst.go"), []byte("//go:build dst\n\npackage reopen\nfunc F() {}\nfunc G(int) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ask(t, s, second)
	s.Quiesce()
	recs := resolutioncache.Load(dir)
	if len(recs) != 2 {
		t.Fatalf("reopening did not publish its current answer: %v; %v", recs, s.Notices())
	}
	for _, rec := range recs {
		if rec.Symbol == second && rec.Views[1].Fingerprint == initialSecond {
			t.Fatal("reopening published the earlier opening's view fingerprint")
		}
	}
	warm, err := NewServed(t.Context(), dir, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	defer warm.Close()
	if warm.ServedCount() != 1 || warm.served[second].Symbol != second {
		t.Fatalf("reopening did not bind the persistently changed view: %v", warm.Notices())
	}
	oldHook := commandHook
	t.Cleanup(func() { commandHook = oldHook })
	commandHook = func(name string, args []string) { t.Errorf("held answer spawned %s %v", name, args) }
	ask(t, s, second)
}

func TestResolutionMultiViewServingMatchesPrecedenceOracle(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	allSels := []buildSelection{
		{},
		{tags: []string{"dst"}},
		{tags: []string{"integration"}},
	}
	subj := gofresh.Subject{Package: "example.com/p", Symbol: "F"}
	const sym = "example.com/p.F"

	checkCell := func(resIdx int, afterMask int, swapOrder bool, states []int) {
		recViews := make([]resolutioncache.ViewEntry, len(allSels))
		for i, sel := range allSels {
			present := false
			if i == resIdx {
				present = true
			} else if i > resIdx {
				present = (afterMask>>(i-resIdx-1))&1 == 1
			}
			var fp gofresh.Fingerprint
			if present {
				fp = fingerprintFor("a")
			}
			recViews[i] = resolutioncache.ViewEntry{
				Selection:   sel.key(),
				Present:     present,
				Fingerprint: fp,
			}
		}
		rec := resolutioncache.Record{
			Selection:    allSels[resIdx].key(),
			Symbol:       sym,
			Views:        recViews,
			Resolution:   "resolved",
			Shape:        "shape",
			Package:      "example.com/p",
			WitnessClass: "example",
		}
		if !resolutioncache.AdmitsRecord(rec) {
			t.Fatalf("constructed invalid baseline record: %+v", rec)
		}

		curSels := slices.Clone(allSels)
		if swapOrder {
			curSels[1], curSels[2] = curSels[2], curSels[1]
		}
		curCaps := make(map[string]sourceViewCapture, len(curSels))
		inventory := make(map[string]map[string]bool, len(curSels))
		for i, sel := range curSels {
			key := sel.key()
			st := states[i]
			pkgDeclared := st != 0
			inventory[key] = map[string]bool{"example.com/p": pkgDeclared}
			cap := sourceViewCapture{
				present:    map[gofresh.Subject]gofresh.Fingerprint{},
				absent:     map[gofresh.Subject]bool{},
				failedPkgs: map[string]error{},
				probed:     map[gofresh.Subject]bool{subj: true},
			}
			switch st {
			case 0, 1:
				cap.absent[subj] = true
			case 2:
				cap.present[subj] = fingerprintFor("a")
			case 3:
				cap.present[subj] = fingerprintFor("d")
			case 4:
				// Uncaptured (neither present nor absent).
			}
			curCaps[key] = cap
		}

		anyDeclared := false
		for _, m := range inventory {
			if m["example.com/p"] {
				anyDeclared = true
				break
			}
		}
		wantServe := anyDeclared && recordMatchesSelections(rec, curSels) && inventory[rec.Selection]["example.com/p"]
		if wantServe {
			for _, v := range rec.Views {
				cap := curCaps[v.Selection]
				if v.Present {
					fp, ok := cap.present[subj]
					if !inventory[v.Selection]["example.com/p"] || !ok || fp != v.Fingerprint {
						wantServe = false
						break
					}
				} else if _, nowPresent := cap.present[subj]; nowPresent || !cap.absent[subj] {
					wantServe = false
					break
				}
			}
		}

		pkg := ""
		if anyDeclared {
			pkg = "example.com/p"
		}
		_, gotServe := precheckCandidateRecord(rec, pkg, anyDeclared, curSels, inventory)
		if gotServe {
			for i, sel := range curSels {
				if _, _, keep := checkCandidateInView(rec, i, sel.key(), curCaps[sel.key()]); !keep {
					gotServe = false
					break
				}
			}
		}
		if gotServe != wantServe {
			t.Fatalf("gotServe=%v, wantServe=%v for rec=%+v curSels=%v states=%v", gotServe, wantServe, rec, curSels, states)
		}
	}

	// Exhaustive enumeration of the 3-view precedence and state matrix.
	for resIdx := range len(allSels) {
		maxMask := 1 << (len(allSels) - 1 - resIdx)
		for mask := range maxMask {
			for _, swap := range []bool{false, true} {
				for s0 := range 5 {
					for s1 := range 5 {
						for s2 := range 5 {
							checkCell(resIdx, mask, swap, []int{s0, s1, s2})
						}
					}
				}
			}
		}
	}

	// Randomized sampling across arbitrary subsets of states.
	rapid.Check(t, func(rt *rapid.T) {
		resIdx := rapid.IntRange(0, len(allSels)-1).Draw(rt, "resIdx")
		mask := rapid.IntRange(0, 3).Draw(rt, "afterMask")
		swap := rapid.Bool().Draw(rt, "swapOrder")
		states := []int{
			rapid.IntRange(0, 4).Draw(rt, "s0"),
			rapid.IntRange(0, 4).Draw(rt, "s1"),
			rapid.IntRange(0, 4).Draw(rt, "s2"),
		}
		checkCell(resIdx, mask, swap, states)
	})
}
