package golang

import (
	"context"
	"fmt"
	"slices"

	"github.com/greatliontech/gofresh"
)

// selectedContext owns the configuration of one selected view. Its private
// slices and maps remain read-only to consumers after preparation. Sharing
// configuration does not share source observations.
type selectedContext struct {
	root, directory, key string
	selection            buildSelection
	members              []string
	modules              map[string]string
	env, flags           []string
}

func prepareSelectedContext(root string, sel buildSelection) (*selectedContext, error) {
	sel.tags = slices.Clone(sel.tags)
	members, err := sel.members(root)
	if err != nil {
		return nil, err
	}
	modules, err := memberModules(root, members)
	if err != nil {
		return nil, err
	}
	base, err := goworkModeEnv(root, sel.workspaceMode)
	if err != nil {
		return nil, err
	}
	return &selectedContext{
		root: root, directory: sel.directory(root), key: sel.key(), selection: sel,
		members: members, modules: modules,
		env: selectionViewEnv(base, sel), flags: selectionViewFlags(sel),
	}, nil
}

type endpointSelection struct {
	selection  buildSelection
	view       *selectedContext
	err        error
	session    *gofresh.Session
	sessionErr error
	hasSession bool
}

// selectionEndpoint is one observation boundary. It prepares each view once,
// including a preparation failure, and memoizes one gofresh.Session per view
// within the endpoint. Source generations live in each Snapshot, never on
// Session; endpoints stay local to construction, each child opening, and each
// close publication so configuration re-observation stays fresh.
type selectionEndpoint struct {
	ctx        context.Context
	root       string
	selections map[string]*endpointSelection
}

func newSelectionEndpoint(ctx context.Context, root string, selections []buildSelection) *selectionEndpoint {
	e := &selectionEndpoint{
		ctx:        ctx,
		root:       root,
		selections: make(map[string]*endpointSelection, len(selections)),
	}
	for _, sel := range selections {
		sel.tags = slices.Clone(sel.tags)
		e.selections[sel.key()] = &endpointSelection{selection: sel}
	}
	return e
}

func (e *selectionEndpoint) selected(key string) (*selectedContext, error) {
	s, ok := e.selections[key]
	if !ok {
		return nil, fmt.Errorf("unknown resolution selection %q", key)
	}
	if s.view == nil && s.err == nil {
		s.view, s.err = prepareSelectedContext(e.root, s.selection)
	}
	return s.view, s.err
}

func (e *selectionEndpoint) session(view *selectedContext, sample toolchainSample) (*gofresh.Session, error) {
	s, ok := e.selections[view.key]
	if !ok {
		return selectionEngine(e.ctx, view, sample)
	}
	if !s.hasSession {
		s.session, s.sessionErr = selectionEngine(e.ctx, view, sample)
		s.hasSession = true
	}
	return s.session, s.sessionErr
}
