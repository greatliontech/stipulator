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

// Owned is a verify.Backend whose go/packages symbol loading runs in an
// owned resolver child: this binary self-exec'd as the hidden resolver
// subcommand, spawned through the same owned-cancellation machinery as
// every other child of Go policy work, so the package launcher and its
// entire descendant tree — every go list, compile, and VCS subprocess —
// terminates with the operation's cancellation (REQ-go-owned-processes).
// The in-process implementation stays: the child runs NewContext; the
// parent speaks the JSON-lines resolver protocol over the child's stdio.
//
// The child is spawned lazily on first use and dies with ctx, with an
// explicit Close, or with the parent process (its stdin pipe closes and
// the serve loop ends). Requests are serialized — one in flight — which
// matches how every consumer drives the Backend surface: sequentially,
// from one goroutine.
type Owned struct {
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

// NewOwned returns an owned-child backend for the tree rooted at dir,
// self-exec'd from this process's own executable. It fails closed when
// the executable path cannot be resolved: without a self path there is
// no owned child to spawn, and loading in-process instead would silently
// reopen the unowned process boundary.
func NewOwned(ctx context.Context, dir string) (*Owned, error) {
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
	return NewOwnedCommand(ctx, exe, ResolverSubcommand, abs), nil
}

// NewOwnedCommand is NewOwned with an explicit child command line — the
// testable seam; the command must lead the spawned process into
// ServeResolver. Child lifetime is bound to ctx.
func NewOwnedCommand(ctx context.Context, exe string, args ...string) *Owned {
	return &Owned{ctx: ctx, exe: exe, args: args}
}

// ensure spawns the resolver child and completes the handshake; the
// caller holds o.mu.
func (o *Owned) ensure() error {
	if o.err != nil {
		return o.err
	}
	if o.cmd != nil {
		return nil
	}
	cctx, stop := context.WithCancel(o.ctx)
	cmd := commandContext(cctx, o.exe, o.args...)
	// Child diagnostics pass straight through: protocol errors travel on
	// stdout, and a shared capture buffer would race the reaper's Wait.
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		return o.fault(fmt.Errorf("opening stdin pipe: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		return o.fault(fmt.Errorf("opening stdout pipe: %w", err))
	}
	if err := cmd.Start(); err != nil {
		stop()
		return o.fault(fmt.Errorf("starting %s: %w", o.exe, err))
	}
	o.cmd, o.stop, o.stdin = cmd, stop, stdin
	o.enc, o.dec = json.NewEncoder(stdin), json.NewDecoder(stdout)
	// The reaper solely owns Wait: the kill path is the owned-group
	// cancellation configured by commandContext, and Wait afterwards
	// keeps a long-lived parent (the MCP server) from accumulating
	// zombies. cctx ends via the caller's ctx, a fault, or Close.
	go func() {
		<-cctx.Done()
		_ = cmd.Wait()
	}()
	var resp resolverResponse
	if err := o.dec.Decode(&resp); err != nil {
		return o.fault(fmt.Errorf("reading handshake: %w", err))
	}
	if resp.Error != "" {
		// The tree's load error text is carried verbatim; the sticky fault
		// wraps it with the child's provenance, so the rendered error names
		// both the boundary and the cause.
		return o.fault(errors.New(resp.Error))
	}
	if !resp.Ready {
		return o.fault(errors.New("handshake neither ready nor a load error"))
	}
	return nil
}

// fault records the sticky error and kills the child's process group;
// the caller holds o.mu.
func (o *Owned) fault(err error) error {
	o.err = fmt.Errorf("owned resolver child: %w", err)
	if o.stop != nil {
		o.stop()
	}
	return o.err
}

// roundTrip sends one request and reads its one response, serialized.
func (o *Owned) roundTrip(req resolverRequest) (resolverResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.ensure(); err != nil {
		return resolverResponse{}, err
	}
	if err := o.enc.Encode(req); err != nil {
		return resolverResponse{}, o.fault(fmt.Errorf("writing %s request: %w", req.Op, err))
	}
	var resp resolverResponse
	if err := o.dec.Decode(&resp); err != nil {
		return resolverResponse{}, o.fault(fmt.Errorf("reading %s response: %w", req.Op, err))
	}
	return resp, nil
}

// Resolve implements verify.Backend through the resolver child. A child
// transport fault is a verification error exactly as an unloadable tree
// is: never an absence.
func (o *Owned) Resolve(symbol string) (verify.Resolution, string, error) {
	resp, err := o.roundTrip(resolverRequest{Op: "resolve", Symbol: symbol})
	if err != nil {
		return verify.NotFound, "", err
	}
	res, ok := resolutionFromWire(resp.Resolution)
	if !ok {
		return verify.NotFound, "", fmt.Errorf("owned resolver child: unknown resolution %q for %s", resp.Resolution, symbol)
	}
	if resp.Error != "" {
		return res, resp.Shape, errors.New(resp.Error)
	}
	return res, resp.Shape, nil
}

// WitnessClass implements verify.WitnessClassifier through the resolver
// child. The interface admits no error return, so a faulted transport
// reads as the weakest class — example, never an upgraded proof or
// property — while the fault itself surfaces as a verification error
// from Resolve, which every classifying run also performs per binding.
func (o *Owned) WitnessClass(symbol string) verify.WitnessClass {
	resp, err := o.roundTrip(resolverRequest{Op: "witnessclass", Symbol: symbol})
	if err != nil {
		return verify.ExampleWitness
	}
	if c, ok := classFromWire(resp.Class); ok {
		return c
	}
	// An unrecognized wire class is a protocol fault like any other: record
	// it sticky so the run's next Resolve surfaces a problem instead of the
	// weakest class standing in silently.
	o.mu.Lock()
	if o.err == nil {
		_ = o.fault(fmt.Errorf("unknown witness class %q", resp.Class))
	}
	o.mu.Unlock()
	return verify.ExampleWitness
}

// Slice implements verify.Slicer through the resolver child.
func (o *Owned) Slice(symbols []string) ([]verify.Decl, error) {
	resp, err := o.roundTrip(resolverRequest{Op: "slice", Symbols: symbols})
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

// Close terminates the resolver child's process group; the reaper
// collects it. Safe on a client that never spawned; the client is
// unusable afterwards.
func (o *Owned) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stop != nil {
		o.stop()
	}
	if o.err == nil {
		o.err = errors.New("owned resolver child: closed")
	}
	return nil
}
