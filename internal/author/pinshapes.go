package author

import (
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// ResolveShapes resolves the current shape hash of every binding whose
// requirement is in wanted (nil means all). A per-symbol resolution
// fault is reported through onFault and the symbol skipped — the one
// divergence the two serving surfaces used to hide from each other:
// swallowing the fault silently turns "shape mismatch" back into the
// quiescence claim the pin reporting contract exists to kill
// (REQ-pin-backfill).
func ResolveShapes(store *records.Store, backends map[string]verify.Backend, wanted map[string]bool, onFault func(symbol string, err error)) map[string]string {
	shapes := map[string]string{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if wanted != nil && !wanted[b.GetRequirementId()] {
				continue
			}
			be, ok := backends[b.GetBackend()]
			if !ok {
				continue
			}
			res, shape, err := be.Resolve(b.GetSymbol())
			switch {
			case err != nil:
				if onFault != nil {
					onFault(b.GetSymbol(), err)
				}
			case res == verify.Resolved:
				shapes[records.ShapeKey(b.GetBackend(), b.GetSymbol())] = shape
			}
		}
	}
	return shapes
}
