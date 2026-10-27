package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"

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
	_, _, _ = s.Resolve("example.com/certificate/bad.F")
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

func TestWarmGraphsBatchesSelectionOwners(t *testing.T) {
	neutralAmbient(t)
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/batch\n\ngo 1.27\n",
		"shared/shared.go": "package shared\nfunc V() int { return 1 }\n",
		"shared/embed.txt": "test-only embedded data\n",
		"shared/s_test.go": "package shared\nimport (_ \"embed\"; \"testing\")\n//go:embed embed.txt\nvar data string\nfunc TestS(*testing.T) { _ = data }\n",
		"shared/x_test.go": "package shared_test\nimport (_ \"embed\"; \"testing\")\n//go:embed embed.txt\nvar xdata string\nfunc TestX(*testing.T) { _ = xdata }\n",
		"a/a.go":           "package a\nimport \"example.com/batch/shared\"\nfunc A() int { return shared.V() }\n",
		"a/a_test.go":      "package a\nimport \"testing\"\nfunc TestA(*testing.T) {}\n",
		"a/a_ext_test.go":  "package a_test\nimport (\"testing\"; \"example.com/batch/a\")\nfunc TestAExt(*testing.T) { _ = a.A() }\n",
		"b/b.go":           "package b\nimport \"example.com/batch/shared\"\nfunc B() int { return shared.V() }\n",
	})
	sels, _, err := policyBuildSelections(dir)
	if err != nil {
		t.Fatal(err)
	}
	ep := newSelectionEndpoint(t.Context(), dir, sels)
	view, err := ep.selected(sels[0].key())
	if err != nil {
		t.Fatal(err)
	}
	owners := []string{"example.com/batch/shared", "example.com/batch/a", "example.com/batch/b"}
	oldHook := commandHook
	t.Cleanup(func() { commandHook = oldHook })
	listSpawns := 0
	commandHook = func(name string, args []string) {
		if name == "go" && strings.Contains(strings.Join(args, " "), "-deps") {
			listSpawns++
		}
	}
	ep.warmGraphs(view, owners)
	if listSpawns != 1 {
		t.Fatalf("warmGraphs spawned %d go list -deps commands for %d owners, want 1", listSpawns, len(owners))
	}
	for _, owner := range owners {
		got, err := ep.graph(view, owner)
		if err != nil {
			t.Fatalf("warmed graph(%s): %v", owner, err)
		}
		want, err := sourceGraph(t.Context(), view.directory, view.env, view.flags, owner, &sourceFiles{ctx: t.Context(), digests: map[string]string{}})
		if err != nil {
			t.Fatalf("sourceGraph(%s): %v", owner, err)
		}
		if got != want {
			t.Fatalf("graph(%s) = %q, want %q", owner, got, want)
		}
	}
}

func referenceSelectSourceGraphs(ctx context.Context, pkgs []string, allRows []sourcePackageRow, files *sourceFiles) (map[string]sourceGraphResult, error) {
	byImportPath := make(map[string]sourcePackageRow, len(allRows))
	for _, row := range allRows {
		if row.ImportPath == "" {
			return nil, fmt.Errorf("source graph: missing or repeated package identity %q", row.ImportPath)
		}
		if row.ForTest == "" && strings.Contains(row.ImportPath, " [") {
			return nil, fmt.Errorf("source graph: per-main PGO split on %q requires isolated package query", row.ImportPath)
		}
		if _, dup := byImportPath[row.ImportPath]; dup {
			return nil, fmt.Errorf("source graph: missing or repeated package identity %q", row.ImportPath)
		}
		byImportPath[row.ImportPath] = row
	}
	out := make(map[string]sourceGraphResult, len(pkgs))
	for _, pkg := range pkgs {
		needed := map[string]bool{}
		for _, row := range allRows {
			if row.ImportPath == pkg || row.ForTest == pkg || row.ImportPath == pkg+".test" {
				needed[row.ImportPath] = true
				for _, dep := range row.Deps {
					needed[dep] = true
				}
			}
		}
		pkgRows := make([]sourcePackageRow, 0, len(needed))
		var missingImp string
		for imp := range needed {
			row, ok := byImportPath[imp]
			if !ok {
				if missingImp == "" || imp < missingImp {
					missingImp = imp
				}
				continue
			}
			pkgRows = append(pkgRows, row)
		}
		if missingImp != "" {
			out[pkg] = sourceGraphResult{err: fmt.Errorf("source graph omitted dependency %q", missingImp)}
			continue
		}
		digest, err := digestSourceGraph(ctx, pkg, pkgRows, files)
		out[pkg] = sourceGraphResult{digest: digest, err: err}
	}
	return out, nil
}

func TestSelectSourceGraphsMatchesReferenceSelector(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-resolution-freshness")
	ownersPool := []string{"example.com/a", "example.com/b", "example.com/c", "example.com/shared"}
	rowGen := rapid.Custom(func(rt *rapid.T) sourcePackageRow {
		kind := rapid.IntRange(0, 6).Draw(rt, "kind")
		base := rapid.SampledFrom(ownersPool).Draw(rt, "base")
		var imp, forTest string
		switch kind {
		case 0:
			imp = base
		case 1:
			imp = base + " [" + base + ".test]"
			forTest = base
		case 2:
			imp = base + "_test [" + base + ".test]"
			forTest = base
		case 3:
			imp = base + ".test"
		case 4:
			imp = "example.com/dep/" + rapid.SampledFrom([]string{"x", "y", "z"}).Draw(rt, "dep")
		case 5:
			// Per-main PGO split without ForTest.
			imp = base + " [cmd/pgo]"
		case 6:
			imp = ""
		}
		deps := rapid.SliceOfN(rapid.SampledFrom([]string{"example.com/shared", "example.com/dep/x", "example.com/missing"}), 0, 2).Draw(rt, "deps")
		return sourcePackageRow{
			ImportPath: imp,
			Name:       "p",
			ForTest:    forTest,
			Deps:       deps,
		}
	})
	rapid.Check(t, func(rt *rapid.T) {
		rows := rapid.SliceOfN(rowGen, 0, 8).Draw(rt, "rows")
		pkgs := rapid.SliceOfN(rapid.SampledFrom(ownersPool), 0, 4).Draw(rt, "pkgs")
		ctx := context.Background()
		if rapid.Bool().Draw(rt, "cancel") {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		filesA := &sourceFiles{ctx: ctx, digests: map[string]string{}}
		filesB := &sourceFiles{ctx: ctx, digests: map[string]string{}}
		got, gotErr := selectSourceGraphs(ctx, pkgs, rows, filesA)
		want, wantErr := referenceSelectSourceGraphs(ctx, pkgs, rows, filesB)
		if (gotErr != nil) != (wantErr != nil) {
			rt.Fatalf("err = %v, want %v", gotErr, wantErr)
		}
		if gotErr != nil {
			if gotErr.Error() != wantErr.Error() {
				rt.Fatalf("err = %q, want %q", gotErr.Error(), wantErr.Error())
			}
			return
		}
		if len(got) != len(want) {
			rt.Fatalf("len(got) = %d, len(want) = %d", len(got), len(want))
		}
		for _, pkg := range pkgs {
			g, w := got[pkg], want[pkg]
			if g.digest != w.digest || (g.err != nil) != (w.err != nil) || (g.err != nil && g.err.Error() != w.err.Error()) {
				rt.Fatalf("pkg %s: got (%q, %v), want (%q, %v)", pkg, g.digest, g.err, w.digest, w.err)
			}
		}
	})
}
