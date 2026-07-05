// Package verify checks the committed records against the compiled corpus
// and, through language backends, against the code.
//
// Problems are verification errors — malformed or dangling records,
// unloadable trees, claims against generated files — and always fail a
// run. Everything else is reported as per-binding data (resolution
// outcome, pin and shape state): those facts feed the coverage buckets,
// where gap records may excuse them, so this layer never hard-fails on
// them. The package defines the Backend interface but depends on no
// backend implementation.
package verify

import (
	"fmt"
	"sort"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
)

// Problem is a record inconsistency; any problem fails verification.
type Problem struct {
	Path    string
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Message }

// Resolution classifies a backend's answer for a symbol reference.
type Resolution int

const (
	// Unverified: no backend was available for the binding.
	Unverified Resolution = iota
	// Resolved: the symbol exists; its shape hash accompanies it.
	Resolved
	// NotFound: the symbol does not exist — the binding is broken.
	NotFound
	// GeneratedFile: the symbol lives in a generated file; the claim
	// belongs on the generating artifact.
	GeneratedFile
)

// ShapeState classifies a resolved binding's shape pin.
type ShapeState int

const (
	// ShapeUnknown: the binding did not resolve, so no shape comparison
	// happened.
	ShapeUnknown ShapeState = iota
	// ShapeUnpinned: no shape hash recorded yet — stale, awaiting pin.
	ShapeUnpinned
	// ShapeMatch: the pinned shape equals the symbol's current shape.
	ShapeMatch
	// ShapeMismatch: the symbol's shape moved — the binding is broken
	// until re-verified and re-pinned.
	ShapeMismatch
)

// BindingResult is the verified state of one binding: the facts the
// coverage buckets are computed from.
type BindingResult struct {
	Path          string
	RequirementId string
	Symbol        string
	Backend       string
	Role          stipulatorv1.BindingRole
	// ContentPinned reports whether the content-hash pin matches the
	// requirement's current hash.
	ContentPinned bool
	Resolution    Resolution
	Shape         ShapeState
}

// Backend verifies symbol references for one language. Implementations
// live outside this package: the core never depends on a backend.
type Backend interface {
	// Resolve checks a symbol reference and, when resolved, returns the
	// symbol's current shape hash. A returned error is a verification
	// error (e.g. the tree fails to load), never an absence.
	Resolve(symbol string) (Resolution, string, error)
}

// Report is the outcome of a verification run.
type Report struct {
	Problems []Problem
	// Results holds the verified state of every well-formed binding, in
	// store order.
	Results []BindingResult
	// Pinned counts bindings whose content-hash pin matches the current
	// corpus; Stale counts bindings whose pin is unset or differs.
	Pinned, Stale int
	// ShapePinned, ShapeUnpinned, and ShapeMismatch count resolved
	// bindings by shape-pin state; Broken counts bindings whose symbol
	// did not resolve; Unverified counts bindings whose backend has no
	// verifier in this run.
	ShapePinned, ShapeUnpinned, ShapeMismatch, Broken, Unverified int
}

// Run checks the store against the compiled spec, resolving symbols
// through the supplied backends (keyed by backend name; nil skips all
// symbol resolution).
func Run(spec *stipulatorv1.Spec, store *records.Store, backends map[string]Backend) *Report {
	hashes := map[string]string{}
	for _, r := range spec.GetRequirements() {
		hashes[r.GetId()] = r.GetContentHash()
	}

	rep := &Report{}
	problem := func(path, format string, args ...any) {
		rep.Problems = append(rep.Problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	seen := map[string]bool{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			id := b.GetRequirementId()
			key := id + "|" + b.GetBackend() + "|" + b.GetSymbol() + "|" + b.GetRole().String()
			if seen[key] {
				problem(bf.Path, "duplicate binding: %s %s %s", id, b.GetSymbol(), b.GetRole())
			}
			seen[key] = true

			malformed := false
			if id == "" {
				problem(bf.Path, "binding without requirement_id")
				malformed = true
			}
			if b.GetBackend() == "" {
				problem(bf.Path, "binding for %s has no backend", id)
				malformed = true
			}
			if b.GetSymbol() == "" {
				problem(bf.Path, "binding for %s has no symbol", id)
				malformed = true
			}
			if b.GetRole() == stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
				problem(bf.Path, "binding for %s has no role", id)
				malformed = true
			}
			h, known := hashes[id]
			if id != "" && !known {
				problem(bf.Path, "binding names %s, which is not in the corpus", id)
				malformed = true
			}
			if malformed {
				continue
			}

			result := BindingResult{
				Path:          bf.Path,
				RequirementId: id,
				Symbol:        b.GetSymbol(),
				Backend:       b.GetBackend(),
				Role:          b.GetRole(),
				ContentPinned: b.GetContentHash() == h,
				Resolution:    Unverified,
				Shape:         ShapeUnknown,
			}
			if result.ContentPinned {
				rep.Pinned++
			} else {
				rep.Stale++
			}

			if backend, ok := backends[b.GetBackend()]; ok {
				res, shape, err := backend.Resolve(b.GetSymbol())
				switch {
				case err != nil:
					problem(bf.Path, "resolving %s: %v", b.GetSymbol(), err)
				case res == GeneratedFile:
					// Rejection is a hard rule, never a bucket state.
					result.Resolution = GeneratedFile
					problem(bf.Path, "symbol %s is declared in a generated file; bind the generating artifact instead", b.GetSymbol())
				case res == NotFound:
					result.Resolution = NotFound
					rep.Broken++
				default:
					result.Resolution = Resolved
					switch {
					case b.GetShapeHash() == "":
						result.Shape = ShapeUnpinned
						rep.ShapeUnpinned++
					case b.GetShapeHash() == shape:
						result.Shape = ShapeMatch
						rep.ShapePinned++
					default:
						result.Shape = ShapeMismatch
						rep.ShapeMismatch++
					}
				}
			} else {
				rep.Unverified++
			}
			rep.Results = append(rep.Results, result)
		}
	}

	for _, gf := range store.Gaps {
		id := gf.Gap.GetRequirementId()
		if id == "" {
			problem(gf.Path, "gap without requirement_id")
		} else if _, known := hashes[id]; !known {
			problem(gf.Path, "gap names %s, which is not in the corpus", id)
		}
		if gf.Gap.GetReason() == "" {
			problem(gf.Path, "gap for %s has no reason", id)
		}
		// Landing-condition targets are deliberately not resolved here:
		// exists(...) and covered(...) may name requirements the spec does
		// not hold yet — that prospectiveness is their purpose.
		if !gf.Gap.HasLands() {
			problem(gf.Path, "gap for %s has no landing condition", id)
		}
	}

	sort.Slice(rep.Problems, func(i, j int) bool {
		a, b := rep.Problems[i], rep.Problems[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})
	return rep
}
