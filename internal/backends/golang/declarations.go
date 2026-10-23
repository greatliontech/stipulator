package golang

import "context"

// NewDeclarations prepares typed declaration resolution for the complete named
// symbol set. It neither consumes nor publishes verification records. A nil or
// empty set is explicitly empty; NewWholeTree is the unscoped form. Policy and
// package metadata are read lazily, at the first declaration query.
func NewDeclarations(ctx context.Context, dir string, symbols []string) (*Served, error) {
	s, err := NewWholeTree(ctx, dir)
	if err != nil {
		return nil, err
	}
	s.set = make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		if !s.set[symbol] {
			s.set[symbol] = true
			s.unserved = append(s.unserved, symbol)
		}
	}
	return s, nil
}

func (s *Served) prepareDeclarations() error {
	if !s.whole || s.set == nil || s.inventory != nil {
		return nil
	}
	sels, _, err := policyBuildSelections(s.dir)
	if err != nil {
		return err
	}
	s.sels = sels
	s.inventoryPackages(newSelectionEndpoint(s.ctx, s.dir, sels), s.unserved)
	return s.ctx.Err()
}
