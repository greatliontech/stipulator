package views

import (
	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/proto"
)

// WitnessCacheLoadSummary projects the advisory account to its five-row
// summary budget. It preserves observation order, total refusals and partial
// enumeration metadata, and owns its copy so repeated projections are safe.
func WitnessCacheLoadSummary(account *stipulatorv1.WitnessCacheLoadAccount) *stipulatorv1.WitnessCacheLoadAccount {
	if account == nil {
		return nil
	}
	out := proto.CloneOf(account)
	const rowCap = 5
	if rows := out.GetRefusals(); len(rows) > rowCap {
		out.SetOmitted(out.GetOmitted() + uint64(len(rows)-rowCap))
		out.SetRefusals(rows[:rowCap])
	}
	return out
}
