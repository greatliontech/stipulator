package records

import (
	"testing"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"pgregory.net/rapid"
)

// genHardening draws a Hardening record over the full field surface —
// every field the schema carries, so a renderer lagging the schema fails
// here instead of silently dropping a pin on disk.
func genHardening(rt *rapid.T) *stipulatorv1.Hardening {
	rec := &stipulatorv1.Hardening{}
	rec.SetBackend("go")
	rec.SetSymbol(rapid.StringMatching(`example\.com/[a-z]{1,8}\.[A-Z][a-zA-Z]{0,8}`).Draw(rt, "symbol"))
	rec.SetBodyHash(rapid.StringMatching(`[0-9a-f]{64}`).Draw(rt, "bodyHash"))
	rec.SetMutants(int32(rapid.IntRange(0, 50).Draw(rt, "mutants")))
	rec.SetKilled(int32(rapid.IntRange(0, 50).Draw(rt, "killed")))
	// The renderer's contract is omit-when-zero for the optional pins
	// (and the schema is explicit-presence), so the generator sets those
	// fields only when non-zero — exactly what the writer produces.
	if d := int32(rapid.IntRange(0, 5).Draw(rt, "discarded")); d != 0 {
		rec.SetDiscarded(d)
	}
	var pins []*stipulatorv1.Witness
	for i := range rapid.IntRange(0, 3).Draw(rt, "nWitnessPins") {
		w := &stipulatorv1.Witness{}
		w.SetSymbol(rapid.StringMatching(`example\.com/[a-z]{1,8}\.Test[A-Z][a-z]{0,8}`).Draw(rt, "wsym"))
		w.SetBodyHash(rapid.StringMatching(`[0-9a-f]{64}`).Draw(rt, "whash"))
		pins = append(pins, w)
		_ = i
	}
	rec.SetWitnessPins(pins)
	if ops := rapid.SampledFrom([]string{"", "go/1", "go/2"}).Draw(rt, "operators"); ops != "" {
		rec.SetOperators(ops)
	}
	if bl := int32(rapid.IntRange(0, 500).Draw(rt, "bodyLine")); bl != 0 {
		rec.SetBodyLine(bl)
	}
	if bg := int32(rapid.IntRange(0, 20).Draw(rt, "budget")); bg != 0 {
		rec.SetBudget(bg)
	}
	if tc := rapid.SampledFrom([]string{"", "go1.26.4 linux/amd64", "go1.25 darwin/arm64"}).Draw(rt, "toolchain"); tc != "" {
		rec.SetToolchain(tc)
	}
	var survivors []*stipulatorv1.MutationSurvivor
	for i := range rapid.IntRange(0, 3).Draw(rt, "nSurvivors") {
		s := &stipulatorv1.MutationSurvivor{}
		s.SetPosition(rapid.StringMatching(`[a-z]{1,6}\.go:[1-9][0-9]{0,2}:[1-9][0-9]?`).Draw(rt, "pos"))
		s.SetOperator(rapid.SampledFrom([]string{"== -> !=", "zero return", "drop assignment"}).Draw(rt, "op"))
		survivors = append(survivors, s)
		_ = i
	}
	rec.SetSurvivors(survivors)
	var attested []*stipulatorv1.MutationAttestation
	for i := range rapid.IntRange(0, 2).Draw(rt, "nAttested") {
		a := &stipulatorv1.MutationAttestation{}
		a.SetPosition(rapid.StringMatching(`[a-z]{1,6}\.go:[1-9][0-9]{0,2}:[1-9][0-9]?`).Draw(rt, "apos"))
		a.SetOperator(rapid.SampledFrom([]string{"== -> !=", "force true"}).Draw(rt, "aop"))
		a.SetReason(rapid.StringMatching(`[a-z ]{3,30}`).Draw(rt, "reason"))
		attested = append(attested, a)
		_ = i
	}
	rec.SetAttested(attested)
	return rec
}

// TestPropHardeningRenderRoundTrip quantifies renderer fidelity and
// determinism for the one persisted measurement: rendering is
// byte-identical across runs, and every field written survives a parse
// round-trip — a renderer that silently drops a schema field (the exact
// drift that once swallowed the toolchain pin before a test caught it)
// fails the equality, not the filesystem.
func TestPropHardeningRenderRoundTrip(t *testing.T) {
	stipulate.Covers(t, "REQ-core-determinism", "REQ-harden-records")
	rapid.Check(t, func(rt *rapid.T) {
		var recs []*stipulatorv1.Hardening
		for range rapid.IntRange(1, 3).Draw(rt, "nRecs") {
			recs = append(recs, genHardening(rt))
		}
		a := RenderHardening(recs)
		b := RenderHardening(recs)
		if string(a) != string(b) {
			rt.Fatalf("rendering differs across identical runs:\n%s\n---\n%s", a, b)
		}
		parsed := &stipulatorv1.HardeningSet{}
		if err := prototext.Unmarshal(a, parsed); err != nil {
			rt.Fatalf("rendered sheet does not parse: %v\n%s", err, a)
		}
		want := &stipulatorv1.HardeningSet{}
		want.SetRecords(recs)
		if !proto.Equal(want, parsed) {
			rt.Fatalf("round-trip lost fields:\nwant %v\ngot  %v\nrendered:\n%s", want, parsed, a)
		}
	})
}

// TestHardeningGeneratorKeepsPaceWithSchema is the tripwire for the
// generator half of the renderer-drift class: a field added to the
// Hardening schema must be drawn by genHardening (and so exercised by the
// round-trip property) before this list admits it — a silently lagging
// generator is a silent coverage cap.
func TestHardeningGeneratorKeepsPaceWithSchema(t *testing.T) {
	stipulate.Covers(t, "REQ-harden-records")
	drawn := map[protoreflect.FieldNumber]bool{
		2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true,
		10: true, 11: true, 12: true, 13: true, 14: true, 15: true,
	}
	fields := (&stipulatorv1.Hardening{}).ProtoReflect().Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !drawn[fd.Number()] {
			t.Errorf("Hardening field %d (%s) is not drawn by genHardening; extend the generator and the renderer together", fd.Number(), fd.Name())
		}
	}
}
