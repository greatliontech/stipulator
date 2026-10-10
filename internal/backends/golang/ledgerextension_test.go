package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// A native observed recording retains its producer support through repeated
// extensions, a store reload and a separate checker process. The last arm moves
// the tree after the provisional check to exercise the real serve-close gate.
func TestNativeLedgerExtensionPersistsAcrossRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("executes native producers and reloads analysis views")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-carve-out")
	ctx := context.Background()
	subject := gofresh.Subject{Package: "example.com/ledgergrowth", Symbol: "TestEnv"}
	newView := func(dir string, env []string) *gofresh.View {
		t.Helper()
		engine, err := gofresh.New(gofresh.WithDir(dir), gofresh.WithEnv(env...), gofresh.WithDeferredCheckClose())
		if err != nil {
			t.Fatal(err)
		}
		view, err := engine.NewViewFor(ctx, []gofresh.Subject{subject}, dir, gofresh.CodeResult)
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	if dir := os.Getenv("STIPULATOR_LEDGER_RESTART"); dir != "" {
		records := witnesscache.Load(t.Context(), dir)
		if len(records) == 0 {
			t.Fatal("restart lost recording")
		}
		rec := records[0]
		if rec.Fingerprint.InertTestVariantApplicability.Strategy != gofresh.InertTestVariantExtension || witnesscache.LoadLedger(dir, rec) == nil {
			t.Fatal("restart lost effective ledger coordinate")
		}
		view := newView(dir, os.Environ())
		verdict, err := view.CheckObserved(ctx, rec.Fingerprint, subject)
		if err != nil || verdict.Status != gofresh.Valid {
			t.Fatalf("restart: %+v %v", verdict, err)
		}
		if err := view.Validate(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	source := `package ledgergrowth
import ("os"; "testing")
func TestEnv(t *testing.T) { if os.Getenv("LEDGER_VALUE") != "guarded" { t.Fatal("environment") } }
func TestFile(t *testing.T) { _, _ = os.ReadFile("fixture") }
`
	dir := writeModule(t, map[string]string{"go.mod": "module example.com/ledgergrowth\n\ngo 1.26\n", "growth_test.go": source, "fixture": "sibling input"})
	env, err := gotool.EnvForCommand(os.Environ(), dir)
	if err != nil {
		t.Fatal(err)
	}
	env = gotool.SetEnv(env, "LEDGER_VALUE", "guarded")
	producer := newView(dir, env)
	frame := runtimeinput.CaptureProducerFrame(ctx, dir, dir, runtimeinput.FrameOptions{})
	support, err := producer.PrepareOutcomeSupport(ctx, frame, "witness")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := producer.CaptureObserved(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := producer.TestVariantLedger(subject)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "growth.test")
	build := exec.CommandContext(ctx, "go", "test", "-c", "-o", bin, ".")
	build.Dir, build.Env = dir, env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	log := filepath.Join(t.TempDir(), "inputs.log")
	run := exec.CommandContext(ctx, bin, "-test.run=^TestEnv$", "-test.testlogfile="+log)
	run.Dir, run.Env = dir, env
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("run: %s %v", out, err)
	}
	receipt, err := frame.Completion("witness", env, "")
	if err != nil {
		t.Fatal(err)
	}
	obs, reason, err := frame.Observe(ctx, log, runtimeinput.ProducerIngest{Identity: "witness", Env: env, Completion: receipt, Outcome: support})
	if err != nil || reason != "" {
		t.Fatalf("producer: %s %v", reason, err)
	}
	fp, err = producer.AttachObservation(subject, fp, obs)
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	original := fp
	rec := witnesscache.Record{Group: "group", Package: subject.Package, Test: subject.Symbol, Fingerprint: fp, CompartmentLedger: witnesscache.LedgerFromGofresh(ledger), Outcomes: map[string]string{subject.Package + "." + subject.Symbol: "passed"}}
	if err := witnesscache.Install(t.Context(), dir, rec); err != nil {
		t.Fatal(err)
	}
	// This shape needs observed support; ordinary checking is insufficient.
	if v, err := newView(dir, env).Check(ctx, fp, subject); err != nil || v.Status != gofresh.Unverifiable {
		t.Fatalf("ordinary fixture: %+v %v", v, err)
	}
	for i, name := range []string{"TestMore", "TestAgain", "TestDiscarded"} {
		loaded := witnesscache.Load(t.Context(), dir)
		if len(loaded) == 0 {
			t.Fatal("record missing")
		}
		rec = loaded[0]
		if rec.CompartmentLedger != nil {
			t.Fatal("store eagerly loaded ledger")
		}
		source += "\nfunc " + name + "(t *testing.T) { if 2+2 != 4 { t.Fatal(\"arithmetic\") } }\n"
		if err := os.WriteFile(filepath.Join(dir, "growth_test.go"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		view := newView(dir, env)
		verdict, err := view.CheckObserved(ctx, rec.Fingerprint, subject)
		if err != nil || verdict.Status != gofresh.Stale || verdict.Reason != gofresh.ReasonTestVariants {
			t.Fatalf("before extension: %+v %v", verdict, err)
		}
		extended, verdict, err := compartmentGrownRefresh(ctx, dir, view, rec, verdict, subject)
		if err != nil || verdict.Status != gofresh.Valid {
			t.Fatalf("extension: %+v %v", verdict, err)
		}
		unchanged := extended.Fingerprint
		unchanged.InertTestVariantApplicability = gofresh.InertTestVariantApplicability{}
		if unchanged != original || extended.Fingerprint.EffectiveTestVariantClosure() == rec.Fingerprint.EffectiveTestVariantClosure() {
			t.Fatal("producer rewritten or effective endpoint unmoved")
		}
		current, err := view.TestVariantLedger(subject)
		if err != nil || !reflect.DeepEqual(current, extended.CompartmentLedger.ToGofresh()) {
			t.Fatal("replacement lost current ledger", err)
		}
		wg := &witnessGroup{legs: map[string]*packageLeg{subject.Package: {view: view}}, served: []gofresh.Subject{subject}, recorded: map[gofresh.Subject]witnesscache.Record{subject: extended}, refreshed: map[gofresh.Subject]bool{subject: true}, executedWhy: map[gofresh.Subject]string{}}
		if i == 2 {
			afterServedCheckForTest = func(string) {
				moved := strings.Replace(source, `"guarded"`, `"changed"`, 1)
				if err := os.WriteFile(filepath.Join(dir, "growth_test.go"), []byte(moved), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { afterServedCheckForTest = nil })
		}
		drifted, publish, _ := revalidateServed(ctx, wg, subject.Package)
		if i == 2 {
			if len(drifted) != 1 || len(publish) != 0 {
				t.Fatalf("failed close granted pair: drift=%v publish=%d", drifted, len(publish))
			}
			continue
		}
		if len(drifted) != 0 || len(publish) != 1 {
			t.Fatalf("close: drift=%v publish=%d reason=%v", drifted, len(publish), wg.executedWhy)
		}
		if err := witnesscache.Install(t.Context(), dir, publish[0]); err != nil {
			t.Fatal(err)
		}
		checker := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeLedgerExtensionPersistsAcrossRestart$", "-test.count=1")
		checker.Env = gotool.SetEnv(env, "STIPULATOR_LEDGER_RESTART", dir)
		if out, err := checker.CombinedOutput(); err != nil {
			t.Fatalf("restart: %s %v", out, err)
		}
	}
}

// Mixed publication and serving have different closing points: the executed
// sibling publishes at its completion, while the original observed extension
// must still check and close after every execution of the run.
func TestObservedExtensionSurvivesExecutingSiblingPublication(t *testing.T) {
	if testing.Short() {
		t.Skip("executes package witnesses across repeated extensions")
	}
	stipulate.Covers(t, "REQ-evidence-witness-freshness-carve-out", "REQ-evidence-witness-freshness-revalidation")
	neutralAmbient(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := observedEnvironmentModule(t)
	ctx := context.Background()
	requireRun := func(fresh, ran int) {
		t.Helper()
		tr, err := RunWitnesses(ctx, dir, noSeeding{})
		if err != nil {
			t.Fatal(err)
		}
		if tr.Degraded != "" || tr.Fresh != fresh || tr.Ran != ran || tr.Uncached != 0 {
			t.Fatalf("fresh=%d ran=%d uncached=%d degraded=%q; want %d/%d/0; executed=%v uncacheable=%v", tr.Fresh, tr.Ran, tr.Uncached, tr.Degraded, fresh, ran, tr.ExecutedReasons, tr.UncacheableReasons)
		}
	}
	requireRun(0, 1)
	original := witnesscache.Load(t.Context(), dir)
	if len(original) != 1 || original[0].Fingerprint.PurityAssertion != "" || !original[0].Fingerprint.ObservationProof.Observable {
		t.Fatal("fixture has no native observed record", original)
	}
	for i, name := range []string{"TestSibling", "TestAnotherSibling"} {
		text := "package purefix\nimport \"testing\"\nfunc " + name + "(t *testing.T) { if 2+2 != 4 { t.Fatal(\"arithmetic\") } }\n"
		if err := os.WriteFile(filepath.Join(dir, name+"_test.go"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		requireRun(i+1, 1)
		var extended *witnesscache.Record
		for _, rec := range witnesscache.Load(t.Context(), dir) {
			if rec.Test == original[0].Test && rec.Fingerprint.InertTestVariantApplicability.Strategy == gofresh.InertTestVariantExtension {
				copy := rec
				extended = &copy
				break
			}
		}
		if extended == nil || witnesscache.LoadLedger(dir, *extended) == nil {
			t.Fatal("mixed run did not persist extension and ledger")
		}
		fp := extended.Fingerprint
		fp.InertTestVariantApplicability = gofresh.InertTestVariantApplicability{}
		if fp != original[0].Fingerprint {
			t.Fatal("mixed run changed producing evidence")
		}
		requireRun(i+2, 0)
	}
}
