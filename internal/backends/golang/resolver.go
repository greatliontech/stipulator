package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/greatliontech/stipulator/internal/verify"
)

// ResolverSubcommand is argv[1] of an owned resolver child: the hidden
// CLI subcommand a parent stipulator process self-execs to put
// go/packages symbol loading behind an owned, cancellable process
// boundary (REQ-go-owned-processes). Parent and child agree on this one
// name; it is process plumbing, never public CLI surface.
const ResolverSubcommand = "internal-resolve"

// resolverRequest is one parent→child protocol line. Symbol carries the
// resolve and witnessclass subject; Symbols carries the slice subjects.
type resolverRequest struct {
	Op      string   `json:"op"`
	Symbol  string   `json:"symbol,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
}

// resolverResponse is one child→parent protocol line: the handshake
// (ready, or the tree's load error) or one operation's value-shaped
// result. Error alongside Resolution mirrors Resolve's contract, where
// a resolution outcome and a verification error travel together.
type resolverResponse struct {
	Ready      bool           `json:"ready,omitempty"`
	Error      string         `json:"error,omitempty"`
	Resolution string         `json:"resolution,omitempty"`
	Shape      string         `json:"shape,omitempty"`
	Class      string         `json:"class,omitempty"`
	Decls      []resolverDecl `json:"decls,omitempty"`
}

// resolverDecl is verify.Decl on the wire: four strings, value-shaped.
type resolverDecl struct {
	Package     string `json:"package"`
	Name        string `json:"name"`
	Declaration string `json:"declaration"`
	ShapeHash   string `json:"shape_hash"`
}

// resolutionWire renders a resolution for the protocol.
func resolutionWire(r verify.Resolution) string {
	switch r {
	case verify.Resolved:
		return "resolved"
	case verify.NotFound:
		return "not_found"
	case verify.GeneratedFile:
		return "generated_file"
	}
	return "unverified"
}

// resolutionFromWire parses a protocol resolution, reporting whether the
// value is known — an unknown value is a protocol fault the caller must
// refuse, never a silent default.
func resolutionFromWire(s string) (verify.Resolution, bool) {
	switch s {
	case "resolved":
		return verify.Resolved, true
	case "not_found":
		return verify.NotFound, true
	case "generated_file":
		return verify.GeneratedFile, true
	case "unverified":
		return verify.Unverified, true
	}
	return verify.NotFound, false
}

// classWire renders a witness class for the protocol.
func classWire(c verify.WitnessClass) string {
	switch c {
	case verify.AnalyzerProof:
		return "proof"
	case verify.PropertyWitness:
		return "property"
	}
	return "example"
}

// classFromWire parses a protocol witness class, reporting whether the
// value is known.
func classFromWire(s string) (verify.WitnessClass, bool) {
	switch s {
	case "proof":
		return verify.AnalyzerProof, true
	case "property":
		return verify.PropertyWitness, true
	case "example":
		return verify.ExampleWitness, true
	}
	return verify.ExampleWitness, false
}

// ServeResolver is the resolver child's half of the owned symbol-loading
// boundary: it loads the tree rooted at dir in-process (NewContext) and
// serves the Backend surface — resolve, witnessclass, slice — over the
// JSON-lines protocol on r and w. The first response line is the
// handshake: ready, or the tree's load error, propagated verbatim so the
// parent reports it exactly as an in-process load would have. Every
// request line is answered with exactly one response line; the loop ends
// cleanly when r reaches EOF — the parent closed the pipe or exited.
func ServeResolver(ctx context.Context, dir string, r io.Reader, w io.Writer) error {
	enc := json.NewEncoder(w)
	b, err := NewContext(ctx, dir)
	if err != nil {
		if encErr := enc.Encode(resolverResponse{Error: err.Error()}); encErr != nil {
			return errors.Join(err, encErr)
		}
		return err
	}
	if err := enc.Encode(resolverResponse{Ready: true}); err != nil {
		return err
	}
	dec := json.NewDecoder(r)
	for {
		var req resolverRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var resp resolverResponse
		switch req.Op {
		case "resolve":
			res, shape, err := b.Resolve(req.Symbol)
			resp.Resolution = resolutionWire(res)
			resp.Shape = shape
			if err != nil {
				resp.Error = err.Error()
			}
		case "witnessclass":
			resp.Class = classWire(b.WitnessClass(req.Symbol))
		case "slice":
			decls, err := b.Slice(req.Symbols)
			if err != nil {
				resp.Error = err.Error()
			} else {
				resp.Decls = make([]resolverDecl, 0, len(decls))
				for _, d := range decls {
					resp.Decls = append(resp.Decls, resolverDecl{
						Package:     d.Package,
						Name:        d.Name,
						Declaration: d.Declaration,
						ShapeHash:   d.ShapeHash,
					})
				}
			}
		default:
			resp.Error = fmt.Sprintf("unknown resolver op %q", req.Op)
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
}

// ResolverChildMain routes a resolver-child invocation of the current
// process: when argv names the resolver subcommand it serves the
// protocol on stdio and exits, and otherwise it returns immediately.
// The owned client self-execs os.Executable(), which in-process tests
// make the test binary itself — any test binary whose tests reach an
// owned backend must call this from TestMain before running tests, or
// the child invocation would run the test suite instead of a resolver.
func ResolverChildMain() {
	if len(os.Args) != 3 || os.Args[1] != ResolverSubcommand {
		return
	}
	if err := ServeResolver(context.Background(), os.Args[2], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
