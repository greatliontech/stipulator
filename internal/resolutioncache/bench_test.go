package resolutioncache

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/stipulator/internal/witnesscache"
)

func BenchmarkDecodeRecord(b *testing.B) {
	fp := witnesscache.Fingerprint{
		MaximalClosure:     strings.Repeat("a", 32),
		TestVariantClosure: strings.Repeat("b", 32),
		ClosureStrategy:    gofresh.ClosureStrategy,
		ToolchainContent:   "toolchain-content",
		AdmissionPolicy:    "admission-policy",
		Guards:             guard.Guards{Toolchain: "go1.27.0", BuildConfig: strings.Repeat("c", 32)},
		ResultKind:         gofresh.CodeResult,
	}
	cert := Certificate{Derivation: Derivation, Inputs: strings.Repeat("e", 64)}
	rec := Record{
		Selection:    "default",
		Symbol:       "example.com/p.F",
		Fingerprint:  SourceTiers(fp),
		Certificate:  cert,
		Resolution:   "resolved",
		Shape:        strings.Repeat("d", 32),
		Package:      "example.com/p",
		WitnessClass: "proof",
	}
	name, err := rec.FileName()
	if err != nil {
		b.Fatal(err)
	}
	raw, err := json.Marshal(entry{
		Version:      version,
		Selection:    rec.Selection,
		Symbol:       rec.Symbol,
		Fingerprint:  rec.Fingerprint,
		Certificate:  rec.Certificate,
		Resolution:   rec.Resolution,
		Shape:        rec.Shape,
		Package:      rec.Package,
		WitnessClass: rec.WitnessClass,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, ok := decodeRecord(name, raw)
		if !ok || got.Symbol != rec.Symbol {
			b.Fatalf("decodeRecord = (%+v, %v)", got, ok)
		}
	}
}
