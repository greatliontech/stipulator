package prune

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/closure/testvariant"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/stipulator/internal/recordstore"
	"github.com/greatliontech/stipulator/internal/resolutioncache"
	"github.com/greatliontech/stipulator/internal/witnesscache"
	"github.com/greatliontech/stipulator/stipulate"
)

// TestPruneGlobalCachePreservesTelemetryAndRepoState pins that global cache
// reclamation reclaims abandoned witness, ledger, and resolution stores while
// leaving telemetry-off configuration and repository .stipulator/ files untouched.
func TestPruneGlobalCachePreservesTelemetryAndRepoState(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-store-gc")
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	abandonedRepo := filepath.Join(t.TempDir(), "abandoned-repo")
	liveRepo := filepath.Join(t.TempDir(), "live-repo")
	for _, repo := range []string{abandonedRepo, liveRepo} {
		if err := os.MkdirAll(filepath.Join(repo, ".stipulator", "gaps"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gapFile := filepath.Join(liveRepo, ".stipulator", "gaps", "REQ-live.textproto")
	if err := os.WriteFile(gapFile, []byte("requirement_id: \"REQ-live\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	telemetryFile := filepath.Join(cacheDir, "stipulator", "telemetry-off", "abc123", "go", "telemetry", "mode")
	if err := os.MkdirAll(filepath.Dir(telemetryFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(telemetryFile, []byte("off\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	seedRepo := func(repo string) {
		inputs, err := runtimeinput.Merge(repo, nil)
		if err != nil {
			t.Fatal(err)
		}
		fp := witnesscache.Fingerprint{
			MaximalClosure:       strings.Repeat("a", 32),
			TestVariantClosure:   strings.Repeat("b", 32),
			ClosureStrategy:      gofresh.ClosureStrategy,
			DynamicStateStrategy: gofresh.DynamicStateStrategy,
			ToolchainContent:     "tc",
			AdmissionPolicy:      "ap",
			Guards:               guard.Guards{Toolchain: "go1.27.1", BuildConfig: strings.Repeat("c", 32)},
			RuntimeInputs:        inputs.Manifest,
			RuntimeDigest:        inputs.Digest,
			InertTestVariantApplicability: gofresh.InertTestVariantApplicability{
				Strategy:           gofresh.InertTestVariantExtension,
				TestVariantClosure: strings.Repeat("b", 32),
			},
			ResultKind: gofresh.CodeResult,
		}
		if err := witnesscache.Install(t.Context(), repo, witnesscache.Record{
			Group:    "6772702d64696765",
			Package:  "example.com/p",
			Test:     "TestA",
			Outcomes: map[string]string{"example.com/p.TestA": "passed"},
			CompartmentLedger: &witnesscache.CompartmentLedger{
				BindingStrategy: testvariant.BindingStrategy,
				Declarations: []witnesscache.CompartmentDeclaration{
					{File: "p_test.go", Kind: "func", Name: "TestA", Hash: strings.Repeat("0", 32), Package: "p", References: []string{"testing"}},
				},
				FileHeaders: []witnesscache.CompartmentFileHeader{{File: "p_test.go", Hash: strings.Repeat("f", 32), Bindings: &witnesscache.CompartmentFileBindings{Package: "p"}}},
			},
			Fingerprint: fp,
		}); err != nil {
			t.Fatal(err)
		}
		if err := resolutioncache.InstallAll(repo, []resolutioncache.Record{{
			Selection:    "default",
			Symbol:       "example.com/p.TestA",
			Fingerprint:  resolutioncache.SourceTiers(fp),
			Certificate:  resolutioncache.Certificate{Derivation: resolutioncache.Derivation, Inputs: strings.Repeat("e", 64)},
			Resolution:   "resolved",
			Shape:        strings.Repeat("d", 32),
			Package:      "example.com/p",
			WitnessClass: "example",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	seedRepo(abandonedRepo)
	seedRepo(liveRepo)

	if err := os.RemoveAll(abandonedRepo); err != nil {
		t.Fatal(err)
	}

	preview, err := PruneGlobalCache(t.Context(), GlobalCacheOptions{MaxBytes: -1, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Witnesses.AbandonedStores != 1 || preview.Resolutions.AbandonedStores != 1 || preview.ReclaimedBytes() <= 0 {
		t.Fatalf("unexpected preview report: %+v", preview)
	}

	rep, err := PruneGlobalCache(t.Context(), GlobalCacheOptions{MaxBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ReclaimedBytes() != preview.ReclaimedBytes() || rep.RemainingBytes() <= 0 {
		t.Fatalf("unexpected reclamation report: %+v (preview %+v)", rep, preview)
	}
	if got := witnesscache.Load(t.Context(), liveRepo).Records; len(got) != 1 {
		t.Fatalf("live witness store was disturbed: %+v", got)
	}
	if got := resolutioncache.Load(liveRepo); len(got) != 1 {
		t.Fatalf("live resolution store was disturbed: %+v", got)
	}
	if _, err := os.Stat(telemetryFile); err != nil {
		t.Fatalf("telemetry-off configuration was removed: %v", err)
	}
	if _, err := os.Stat(gapFile); err != nil {
		t.Fatalf("repository .stipulator/ gap record was removed: %v", err)
	}
	// Evicting all cache entries via MaxBytes: 0 empties live caches so the next
	// pass must re-execute and re-analyze from scratch without touching .stipulator/.
	// DryRun must agree with the live run on reclaimed bytes and file count,
	// including ledger substore files orphaned by witness eviction.
	zeroPreview, err := PruneGlobalCache(t.Context(), GlobalCacheOptions{MaxBytes: 0, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	zeroRep, err := PruneGlobalCache(t.Context(), GlobalCacheOptions{MaxBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	if zeroRep.ReclaimedBytes() != zeroPreview.ReclaimedBytes() || zeroRep.Witnesses.RemovedFiles != zeroPreview.Witnesses.RemovedFiles || zeroRep.Witnesses.RemovedFiles < 2 {
		t.Fatalf("MaxBytes=0 dry-run vs live-run mismatch: live=%+v preview=%+v", zeroRep, zeroPreview)
	}
	if zeroRep.RemainingBytes() != 0 || len(witnesscache.Load(t.Context(), liveRepo).Records) != 0 || len(resolutioncache.Load(liveRepo)) != 0 {
		t.Fatalf("MaxBytes=0 left cache entries behind: %+v", zeroRep)
	}
	if _, err := os.Stat(gapFile); err != nil {
		t.Fatalf("repository .stipulator/ gap record was removed under MaxBytes=0: %v", err)
	}
	_ = recordstore.DefaultMaxBytes
}
