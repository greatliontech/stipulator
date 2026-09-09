package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/greatliontech/stipulator/internal/verify"
)

// resolverClient is the backend's typed path: a verify.Backend whose
// go/packages symbol loading runs in an owned resolver child — this
// binary self-exec'd as the hidden resolver subcommand, spawned through
// the same owned-cancellation machinery as every other child of Go
// policy work, so the package launcher and its
// entire descendant tree — every go list, compile, and VCS subprocess —
// terminates with the operation's cancellation (REQ-go-owned-processes).
// The in-process implementation stays: the child runs newContext; the
// parent speaks the JSON-lines resolver protocol over the child's stdio.
//
// The child is spawned lazily on first use and dies with ctx, with an
// explicit Close, or with the parent process (its stdin pipe closes and
// the serve loop ends). Requests are serialized — one in flight — which
// matches how every consumer drives the Backend surface: sequentially,
// from one goroutine.
// The client is the one production route to the resolver; every
// consumer reaches it through Served, whose whole-tree form (no symbol
// set) is exactly this client over the whole tree. The optional
// extensions it answers (witness classing, symbol location) must stay
// satisfied, or the consumers degrade silently to their absent-answer
// paths.
var (
	_ verify.Backend           = (*resolverClient)(nil)
	_ verify.SymbolLocator     = (*resolverClient)(nil)
	_ verify.WitnessClassifier = (*resolverClient)(nil)
	_ verify.WitnessSeeding    = (*resolverClient)(nil)
)

type resolverClient struct {
	exe  string
	args []string

	mu sync.Mutex
	// ctx bounds the child's lifetime — a process, not one call — so it
	// is deliberately captured at construction rather than per request.
	ctx   context.Context
	stop  context.CancelFunc
	cmd   *exec.Cmd
	stdin io.WriteCloser
	enc   *json.Encoder
	dec   *json.Decoder
	// err is sticky: once the child faulted or closed, every later call
	// reports it — the client never silently degrades to in-process
	// loading, and never retries against a half-dead child.
	err error
}

// newResolverClient returns a client for the tree rooted at dir,
// self-exec'd from this process's own executable. It fails closed when
// the executable path cannot be resolved: without a self path there is
// no owned child to spawn, and loading in-process instead would silently
// reopen the unowned process boundary.
func newResolverClient(ctx context.Context, dir string) (*resolverClient, error) {
	return newResolverClientScoped(ctx, dir, nil)
}

// newResolverClientScoped is newResolverClient whose child loads
// exactly the named packages and their dependencies instead of the
// whole tree — the
// served resolution's stale remainder (REQ-evidence-resolution-
// freshness); nil patterns load the whole tree.
func newResolverClientScoped(ctx context.Context, dir string, patterns []string) (*resolverClient, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving own executable for the owned resolver child: %w", err)
	}
	// The child inherits this process's working directory, not the
	// tree's, so the root travels absolute.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving tree root %s: %w", dir, err)
	}
	return newResolverClientCommand(ctx, exe, append([]string{ResolverSubcommand, abs}, patterns...)...), nil
}

// newResolverClientCommand is newResolverClient with an explicit child
// command line — the testable seam; the command must lead the spawned
// process into
// ServeResolver. Child lifetime is bound to ctx.
func newResolverClientCommand(ctx context.Context, exe string, args ...string) *resolverClient {
	return &resolverClient{ctx: ctx, exe: exe, args: args}
}

// ensure spawns the resolver child and completes the handshake; the
// caller holds c.mu.
func (c *resolverClient) ensure() error {
	if c.err != nil {
		return c.err
	}
	if c.cmd != nil {
		return nil
	}
	cctx, stop := context.WithCancel(c.ctx)
	cmd := commandContext(cctx, c.exe, c.args...)
	// Child diagnostics pass straight through: protocol errors travel on
	// stdout, and a shared capture buffer would race the reaper's Wait.
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		return c.fault(fmt.Errorf("opening stdin pipe: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		return c.fault(fmt.Errorf("opening stdout pipe: %w", err))
	}
	if err := cmd.Start(); err != nil {
		stop()
		return c.fault(fmt.Errorf("starting %s: %w", c.exe, err))
	}
	c.cmd, c.stop, c.stdin = cmd, stop, stdin
	c.enc, c.dec = json.NewEncoder(stdin), json.NewDecoder(stdout)
	// The reaper solely owns Wait: the kill path is the owned-group
	// cancellation configured by commandContext, and Wait afterwards
	// keeps a long-lived parent (the MCP server) from accumulating
	// zombies. cctx ends via the caller's ctx, a fault, or Close.
	go func() {
		<-cctx.Done()
		_ = cmd.Wait()
	}()
	var resp resolverResponse
	if err := c.dec.Decode(&resp); err != nil {
		return c.fault(fmt.Errorf("reading handshake: %w", err))
	}
	if resp.Error != "" {
		// The tree's load error text is carried verbatim; the sticky fault
		// wraps it with the child's provenance, so the rendered error names
		// both the boundary and the cause.
		return c.fault(errors.New(resp.Error))
	}
	if !resp.Ready {
		return c.fault(errors.New("handshake neither ready nor a load error"))
	}
	return nil
}

// fault records the sticky error and kills the child's process group;
// the caller holds c.mu.
func (c *resolverClient) fault(err error) error {
	c.err = fmt.Errorf("owned resolver child: %w", err)
	if c.stop != nil {
		c.stop()
	}
	return c.err
}

// roundTrip sends one request and reads its one response, serialized.
func (c *resolverClient) roundTrip(req resolverRequest) (resolverResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(); err != nil {
		return resolverResponse{}, err
	}
	if err := c.enc.Encode(req); err != nil {
		return resolverResponse{}, c.fault(fmt.Errorf("writing %s request: %w", req.Op, err))
	}
	var resp resolverResponse
	if err := c.dec.Decode(&resp); err != nil {
		return resolverResponse{}, c.fault(fmt.Errorf("reading %s response: %w", req.Op, err))
	}
	return resp, nil
}

// Resolve implements verify.Backend through the resolver child. A child
// transport fault is a verification error exactly as an unloadable tree
// is: never an absence.
func (c *resolverClient) Resolve(symbol string) (verify.Resolution, string, error) {
	res, shape, _, err := c.ResolveIn(symbol)
	return res, shape, err
}

// ResolveIn is Resolve through the child, naming the resolving build
// selection (Backend.ResolveIn).
func (c *resolverClient) ResolveIn(symbol string) (verify.Resolution, string, string, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "resolve", Symbol: symbol})
	if err != nil {
		return verify.NotFound, "", "", err
	}
	res, ok := resolutionFromWire(resp.Resolution)
	if !ok {
		return verify.NotFound, "", "", fmt.Errorf("owned resolver child: unknown resolution %q for %s", resp.Resolution, symbol)
	}
	if resp.Error != "" {
		return res, resp.Shape, resp.Selection, errors.New(resp.Error)
	}
	return res, resp.Shape, resp.Selection, nil
}

// WitnessClass implements verify.WitnessClassifier through the resolver
// child. The interface admits no error return, so a faulted transport
// reads as the weakest class — example, never an upgraded proof or
// property — while the fault itself surfaces as a verification error
// from Resolve, which every classifying run also performs per binding.
func (c *resolverClient) WitnessClass(symbol string) verify.WitnessClass {
	class, _ := c.WitnessClassVerdict(symbol)
	return class
}

// WitnessClassVerdict implements verify.WitnessClassVerdicts through
// the resolver child: the class plus the example-classification verdict
// reason. A faulted transport reads as the weakest class with no
// verdict, exactly as WitnessClass's no-error contract.
func (c *resolverClient) WitnessClassVerdict(symbol string) (verify.WitnessClass, string) {
	resp, err := c.roundTrip(resolverRequest{Op: "witnessclass", Symbol: symbol})
	if err != nil {
		return verify.ExampleWitness, ""
	}
	if c, ok := classFromWire(resp.Class); ok {
		return c, resp.ClassReason
	}
	// An unrecognized wire class is a protocol fault like any other: record
	// it sticky so the run's next Resolve surfaces a problem instead of the
	// weakest class standing in silently.
	c.mu.Lock()
	if c.err == nil {
		_ = c.fault(fmt.Errorf("unknown witness class %q", resp.Class))
	}
	c.mu.Unlock()
	return verify.ExampleWitness, ""
}

// NeverServe implements verify.WitnessSeeding through the resolver
// child. A transport or child fault is an error for the caller to fail
// closed on — serving degrades to execution — never a silent empty set
// that would serve a random-seeded witness.
func (c *resolverClient) NeverServe(symbols []string) (map[string]string, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "witnessneverserve", Symbols: symbols})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.NeverServes == nil {
		return map[string]string{}, nil
	}
	return resp.NeverServes, nil
}

// SliceFloor implements verify.FloorSlicer through the resolver child.
func (c *resolverClient) SliceFloor(symbols []string, declaredPkgs []string) ([]verify.FloorPackage, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "slicefloor", Symbols: symbols, DeclaredPackages: declaredPkgs})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	floor := make([]verify.FloorPackage, 0, len(resp.Floor))
	for _, f := range resp.Floor {
		floor = append(floor, verify.FloorPackage{Package: f.Package, Disposition: f.Disposition})
	}
	return floor, nil
}

// Slice implements verify.Slicer through the resolver child.
func (c *resolverClient) Slice(symbols []string) ([]verify.Decl, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "slice", Symbols: symbols})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	decls := make([]verify.Decl, 0, len(resp.Decls))
	for _, d := range resp.Decls {
		decls = append(decls, verify.Decl{
			Package:     d.Package,
			Name:        d.Name,
			Declaration: d.Declaration,
			ShapeHash:   d.ShapeHash,
		})
	}
	return decls, nil
}

// SymbolFile is Backend.SymbolFile through the resolver child. Unlike
// the in-process form it returns transport faults explicitly: a preview
// must distinguish "does not resolve" from "the boundary died", or a
// faulted child would silently empty the code-side candidates.
func (c *resolverClient) SymbolFile(symbol string) (string, bool, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "symbolfile", Symbol: symbol})
	if err != nil {
		return "", false, err
	}
	if resp.Error != "" {
		return "", false, errors.New(resp.Error)
	}
	return resp.File, resp.Found, nil
}

// SymbolPackage is Backend.SymbolPackage through the resolver child.
func (c *resolverClient) SymbolPackage(symbol string) (string, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "symbolpackage", Symbol: symbol})
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Package, nil
}

// ReachedPackages is Backend.ReachedPackages through the resolver child.
func (c *resolverClient) ReachedPackages(files []string) (map[string]bool, error) {
	resp, err := c.roundTrip(resolverRequest{Op: "reached", Symbols: files})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	reach := make(map[string]bool, len(resp.Packages))
	for _, p := range resp.Packages {
		reach[p] = true
	}
	return reach, nil
}

// Close terminates the resolver child's process group; the reaper
// collects it. Safe on a client that never spawned; the client is
// unusable afterwards.
func (c *resolverClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop != nil {
		c.stop()
	}
	if c.err == nil {
		c.err = errors.New("owned resolver child: closed")
	}
	return nil
}
