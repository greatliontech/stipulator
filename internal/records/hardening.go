package records

import (
	"fmt"
	"strconv"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// HardeningDir holds mutation kill-sheets — exploration records, never
// gate input.
const HardeningDir = ".stipulator/hardening"

// HardeningPath is the canonical record file for a symbol's kill-sheet:
// the segment is the symbol's package-local tail, so sheets group by
// package file without embedding the module path.
func HardeningPath(symbol string) string {
	seg := symbol
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.ToLower(strings.ReplaceAll(seg, ".", "-"))
	return HardeningDir + "/" + seg + ".textproto"
}

// RenderHardening renders kill-sheets deterministically.
func RenderHardening(recs []*stipulatorv1.Hardening) []byte {
	var b strings.Builder
	b.WriteString(defaultHeader)
	b.WriteString("# proto-message: stipulator.v1.HardeningSet\n")
	for _, rec := range recs {
		b.WriteString("\nrecords {\n")
		fmt.Fprintf(&b, "  backend: %s\n", strconv.Quote(rec.GetBackend()))
		fmt.Fprintf(&b, "  symbol: %s\n", strconv.Quote(rec.GetSymbol()))
		fmt.Fprintf(&b, "  body_hash: %s\n", strconv.Quote(rec.GetBodyHash()))
		fmt.Fprintf(&b, "  mutants: %d\n", rec.GetMutants())
		fmt.Fprintf(&b, "  killed: %d\n", rec.GetKilled())
		if rec.GetDiscarded() > 0 {
			fmt.Fprintf(&b, "  discarded: %d\n", rec.GetDiscarded())
		}
		for _, s := range rec.GetSurvivors() {
			b.WriteString("  survivors {\n")
			fmt.Fprintf(&b, "    position: %s\n", strconv.Quote(s.GetPosition()))
			fmt.Fprintf(&b, "    operator: %s\n", strconv.Quote(s.GetOperator()))
			b.WriteString("  }\n")
		}
		for _, w := range rec.GetWitnesses() {
			fmt.Fprintf(&b, "  witnesses: %s\n", strconv.Quote(w))
		}
		b.WriteString("}\n")
	}
	return []byte(b.String())
}
