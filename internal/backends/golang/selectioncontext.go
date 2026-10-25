package golang

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"

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
	ctx         context.Context
	root        string
	selections  map[string]*endpointSelection
	files       *sourceFiles
	mu          sync.Mutex
	graphs      map[sourceGraphKey]sourceGraphResult
	graphLoader func(context.Context, *selectedContext, string, *sourceFiles) (string, error)
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

func (e *selectionEndpoint) loadGraph(view *selectedContext, owner string) (string, error) {
	if e.graphLoader != nil {
		return e.graphLoader(e.ctx, view, owner, e.files)
	}
	return sourceGraph(e.ctx, view.directory, view.env, view.flags, owner, e.files)
}

func (e *selectionEndpoint) warmGraphs(view *selectedContext, owners []string) {
	var pending []string
	seen := make(map[string]bool, len(owners))
	e.mu.Lock()
	for _, owner := range owners {
		if owner == "" || seen[owner] {
			continue
		}
		seen[owner] = true
		if _, ok := e.graphs[sourceGraphKey{view.key, owner}]; !ok {
			pending = append(pending, owner)
		}
	}
	e.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	if len(pending) == 1 {
		_, _ = e.graph(view, pending[0])
		return
	}
	workers := min(len(pending), max(1, runtime.GOMAXPROCS(0)))
	ch := make(chan string, len(pending))
	for _, owner := range pending {
		ch <- owner
	}
	close(ch)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for owner := range ch {
				var res sourceGraphResult
				res.digest, res.err = e.loadGraph(view, owner)
				e.mu.Lock()
				key := sourceGraphKey{view.key, owner}
				if _, exists := e.graphs[key]; !exists {
					e.graphs[key] = res
				}
				e.mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

func (e *selectionEndpoint) graph(view *selectedContext, owner string) (string, error) {
	key := sourceGraphKey{view.key, owner}
	e.mu.Lock()
	result, ok := e.graphs[key]
	e.mu.Unlock()
	if !ok {
		result.digest, result.err = e.loadGraph(view, owner)
		e.mu.Lock()
		if existing, exists := e.graphs[key]; exists {
			result = existing
		} else {
			e.graphs[key] = result
		}
		e.mu.Unlock()
	}
	return result.digest, result.err
}
