package golang

import (
	"context"
	"fmt"
	"slices"

	"github.com/greatliontech/gofresh/gotool"
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
	base, err := goworkEnv(root)
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
	selection buildSelection
	view      *selectedContext
	err       error
	reader    *gotool.EnvReader
}

type sourceGraphKey struct{ selection, owner string }
type sourceGraphResult struct {
	digest string
	err    error
}

// selectionEndpoint is one observation boundary. It prepares each view once,
// including a preparation failure, and owns fresh certificate file/graph memos
// and lazy certificate environment readers. Native engines always build their
// own independent readers; this is not a coherent generation shared with them.
// Endpoints are local to construction, each child opening, and each key's
// publication. Keeping one on Served would erase the closing observations.
type selectionEndpoint struct {
	ctx        context.Context
	root       string
	selections map[string]*endpointSelection
	files      *sourceFiles
	graphs     map[sourceGraphKey]sourceGraphResult
}

func newSelectionEndpoint(ctx context.Context, root string, selections []buildSelection) *selectionEndpoint {
	e := &selectionEndpoint{
		ctx: ctx, root: root, selections: map[string]*endpointSelection{},
		files:  &sourceFiles{ctx: ctx, digests: map[string]string{}},
		graphs: map[sourceGraphKey]sourceGraphResult{},
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

// certificateReader belongs only to this endpoint's certificate observation.
// Snapshot and closure.NewAt must consume this same reader, including for a
// selected view with no native subject. A native fingerprint cannot replace it.
func (e *selectionEndpoint) certificateReader(view *selectedContext) *gotool.EnvReader {
	s := e.selections[view.key]
	if s.reader == nil {
		s.reader = gotool.NewEnvReader(engineRunner, view.directory, view.env)
	}
	return s.reader
}

func (e *selectionEndpoint) graph(view *selectedContext, owner string) (string, error) {
	key := sourceGraphKey{view.key, owner}
	result, ok := e.graphs[key]
	if !ok {
		result.digest, result.err = sourceGraph(e.ctx, view.directory, view.env, view.flags, owner, e.files)
		e.graphs[key] = result
	}
	return result.digest, result.err
}
