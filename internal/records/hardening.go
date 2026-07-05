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

// HardeningPath is the canonical record file for a requirement's
// kill-sheets.
func HardeningPath(requirement string) string {
	seg := strings.TrimPrefix(strings.ToLower(requirement), "req-")
	return HardeningDir + "/" + seg + ".textproto"
}

// RenderHardening renders kill-sheets deterministically.
func RenderHardening(recs []*stipulatorv1.Hardening) []byte {
	var b strings.Builder
	b.WriteString(defaultHeader)
	b.WriteString("# proto-message: stipulator.v1.HardeningSet\n")
	for _, rec := range recs {
		b.WriteString("\nrecords {\n")
		fmt.Fprintf(&b, "  requirement_id: %s\n", strconv.Quote(rec.GetRequirementId()))
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
		b.WriteString("}\n")
	}
	return []byte(b.String())
}
