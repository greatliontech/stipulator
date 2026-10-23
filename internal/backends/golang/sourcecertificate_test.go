package golang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/verify"
	"github.com/greatliontech/stipulator/stipulate"
)

func publishResolution(t *testing.T, dir, symbol string) {
	t.Helper()
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if res, _, err := s.Resolve(symbol); err != nil || res != verify.Resolved {
		t.Fatalf("initial resolution: %v, %v", res, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	warm, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer warm.Close()
	if warm.ServedCount() != 1 {
		t.Fatalf("unchanged control did not serve: %v; %v", s.Notices(), warm.Notices())
	}
}

// A default-view answer includes the seeding walk of every selected view,
// even when that other view reaches a helper the default body never names.
func TestResolutionCertificateCoversOtherViewHelpers(t *testing.T) {
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
func TestResolutionCertificateCoversEarlierDeclarationAbsence(t *testing.T) {
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
	res, got, gotKey, err := s.ResolveIn(symbol)
	if err != nil || res != verify.Resolved || got != shape || gotKey != "default" {
		t.Fatalf("served wrong declaration: %v %q %q %v, want %q in default", res, got, gotKey, err, shape)
	}
}

func TestResolutionCertificateIsolatesGraphFailures(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness-degrade")
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":       "module example.com/certificate\n\ngo 1.27\n",
		"good/good.go": "package good\nfunc F() {}\n",
	})
	const good = "example.com/certificate/good.F"
	publishResolution(t, dir, good)
	if err := os.Mkdir(filepath.Join(dir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad", "bad.go"), []byte("package bad\nfunc F() { missing() }\nfunc G() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldHook := commandHook
	t.Cleanup(func() { commandHook = oldHook })
	badGraphs := 0
	commandHook = func(name string, args []string) {
		if name == "go" && strings.Contains(strings.Join(args, " "), "-deps") && args[len(args)-1] == "example.com/certificate/bad" {
			badGraphs++
		}
	}
	s, err := NewServed(t.Context(), dir, []string{good, "example.com/certificate/bad.F", "example.com/certificate/bad.G"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 1 || s.served[good].Symbol != good {
		t.Fatalf("independent graph failure discarded good certificate: %v", s.Notices())
	}
	for _, name := range []string{"bad.F", "bad.G"} {
		if !strings.Contains(strings.Join(s.Notices(), "\n"), name) {
			t.Fatalf("graph failure lost affected symbol %s: %v", name, s.Notices())
		}
	}
	if badGraphs != 1 {
		t.Fatalf("failed owner graph queried %d times, want one shared failure", badGraphs)
	}
}

func TestResolutionCertificateBindsClassifierImage(t *testing.T) {
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
	if s.ServedCount() != 0 {
		t.Fatal("another classifier image served the prior derivation")
	}
	if res, _, err := s.Resolve(symbol); err != nil || res != verify.Resolved {
		t.Fatalf("classifier change did not fall back to derivation: %v %v", res, err)
	}
}

func TestResolutionCertificateClosesOtherViewDependencies(t *testing.T) {
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

func TestResolutionCertificateCoversViewPrecedence(t *testing.T) {
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

func TestResolutionCertificateRequiresEveryView(t *testing.T) {
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
	certs, err := s.captureSourceCertificates(newSelectionEndpoint(s.ctx, s.dir, s.sels), []string{symbol})
	if err == nil || !strings.Contains(err.Error(), "missing") || len(certs) != 0 {
		t.Fatalf("a complete default view certified an incomplete union: %v %v", certs, err)
	}
}

func TestResolutionCertificateClosesBeforeServing(t *testing.T) {
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
	old := beforeCertificateCloseForTest
	t.Cleanup(func() { beforeCertificateCloseForTest = old })
	changed := false
	beforeCertificateCloseForTest = func() {
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
}

// A GOENV-only edit changes the certificate environment without changing
// source, exports, configuration-file paths, or the native closing capture.
func TestResolutionCertificateClosesGOENV(t *testing.T) {
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
	old := beforeCertificateCloseForTest
	t.Cleanup(func() { beforeCertificateCloseForTest = old })
	beforeCertificateCloseForTest = func() { write("off") }
	s, err := NewServed(t.Context(), dir, []string{symbol})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ServedCount() != 0 || s.Reasons()[symbol] != "source dependencies moved during serving" {
		t.Fatalf("GOENV change escaped the closing certificate: %v", s.Notices())
	}
}

func TestResolutionCertificateReopensAfterQuiesce(t *testing.T) {
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
	initialSecond := s.openingCertificates[second]
	if initialSecond.Inputs == "" {
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
		if rec.Symbol == second && rec.Certificate == initialSecond {
			t.Fatal("reopening published the earlier opening's certificate")
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
