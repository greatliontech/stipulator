package author

import (
	"sort"

	"github.com/greatliontech/stipulator/internal/records"
)

// BindSymbols returns the complete distinct symbol set for one backend in a
// claim batch. An empty result is an explicit empty scope.
func BindSymbols(claims []BindRequest, backend string) []string {
	set := map[string]bool{}
	for _, claim := range claims {
		if claim.Backend == backend {
			set[claim.Symbol] = true
		}
	}
	return sortedSymbols(set)
}

// PinSymbols returns the symbols whose shapes a named pin judges, for one
// backend. Only the selected requirements contribute declarations.
func PinSymbols(store *records.Store, backend string, wanted map[string]bool) []string {
	set := map[string]bool{}
	for _, file := range store.Bindings {
		for _, binding := range file.Set.GetBindings() {
			if wanted[binding.GetRequirementId()] && binding.GetBackend() == backend {
				set[binding.GetSymbol()] = true
			}
		}
	}
	return sortedSymbols(set)
}

func sortedSymbols(set map[string]bool) []string {
	symbols := make([]string, 0, len(set))
	for symbol := range set {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols
}
