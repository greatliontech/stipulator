package golang

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/greatliontech/gofresh"
)

// ExplainDynamicState derives the refusal chain for a dynamic-state
// culprit a witness reason names - the variable varName declared in
// package pkgPath - against the same policy-scoped views the
// verdicts derive over (gofresh's explain contract). Groups are
// tried in deterministic group-key order; the first group whose view
// yields a chain answers, named by its member invocations (sorted,
// comma-joined) so a caller holding a reason from a different view
// can see the mismatch. A culprit no group's view knows yields an
// empty chain, an empty view name, and no error.
func ExplainDynamicState(ctx context.Context, pc *Capture, pkgPath, varName string) (gofresh.Chain, string, error) {
	dir := pc.dir
	d, err := pc.discover(ctx)
	if err != nil {
		return gofresh.Chain{}, "", err
	}
	for g, subjects := range d.populatedGroups() {
		engine, err := groupEngine(ctx, dir, g)
		if err != nil {
			return gofresh.Chain{}, "", err
		}
		view, err := engine.NewView(ctx, subjects, dir)
		if err != nil {
			return gofresh.Chain{}, "", err
		}
		chain, err := view.ExplainDynamicState(ctx, pkgPath, varName)
		if err != nil {
			return gofresh.Chain{}, "", err
		}
		if chain.Arm != "" {
			names := append([]string(nil), g.invs...)
			sort.Strings(names)
			return chain, strings.Join(names, ","), nil
		}
	}
	return gofresh.Chain{}, "", nil
}

// culpritReason matches the "<pkg>: <pkg>.<var> <verdict text>" tail
// every shared-dynamic-state downgrade carries.
var culpritReason = regexp.MustCompile(`([^\s:]+): ([^\s:]+)\.([\p{L}_][\p{L}\p{Nd}_]*) `)

// CulpritFromReason extracts the dynamic-state culprit a composed
// uncacheable reason names — the package and variable — so a surface
// can explain a verdict from the reason text verbatim, on the CLI and
// the MCP alike.
func CulpritFromReason(reason string) (pkgPath, symbol string, ok bool) {
	for _, m := range culpritReason.FindAllStringSubmatch(reason+" ", -1) {
		if m[1] == m[2] {
			return m[1], m[3], true
		}
	}
	return "", "", false
}

// ExplainRequest is what a surface's explain request settled to: a
// dynamic-state culprit (Package and Symbol), a witness whose seeding
// derivation is asked (Witness), or a reason that is its own
// derivation (Attribution) — exactly one set.
type ExplainRequest struct {
	Package, Symbol string
	Witness         string
	Attribution     string
}

// ResolveExplain settles what a surface's explain request names. A
// witness travels alone. The package and symbol travel together — a
// lone one is refused, the caller typed it for a reason — and name the
// culprit outright. Else the reason is classified: a seeding-family
// reason derives from the witness's body, so it is refused naming the
// witness form; a freshness-library reason yields the culprit parsed
// from its tail, or — carrying none — is its own attribution; a
// reason that is its own attribution answers as such; a spelling no
// class owns is tried as the library's tail passed bare, then refused.
// spelling renders the argument names in the refusals ("--package" on
// the CLI, "package" on the MCP), so both surfaces share one contract
// (REQ-mcp-explain).
func ResolveExplain(reason, pkgPath, symbol, witness string, spelling func(string) string) (ExplainRequest, error) {
	if witness != "" {
		if reason != "" || pkgPath != "" || symbol != "" {
			return ExplainRequest{}, fmt.Errorf("explain: %s travels alone — it names the witness whose seeding is derived", spelling("witness"))
		}
		return ExplainRequest{Witness: witness}, nil
	}
	if (pkgPath == "") != (symbol == "") {
		return ExplainRequest{}, fmt.Errorf("explain: %s and %s travel together", spelling("package"), spelling("symbol"))
	}
	if pkgPath != "" {
		return ExplainRequest{Package: pkgPath, Symbol: symbol}, nil
	}
	if reason == "" {
		return ExplainRequest{}, fmt.Errorf("explain: pass %s to parse, %s for a witness's seeding, or %s and %s", spelling("reason"), spelling("witness"), spelling("package"), spelling("symbol"))
	}
	class, known := classifyReason(reason)
	if known && class.kind() == explainWitness {
		return ExplainRequest{}, fmt.Errorf("explain: a seeding reason derives from the witness's own body; pass %s naming the witness the reason stood beside", spelling("witness"))
	}
	if known && class.kind() == explainSelf {
		return ExplainRequest{Attribution: reason}, nil
	}
	if pkgPath, symbol, ok := CulpritFromReason(reason); ok {
		return ExplainRequest{Package: pkgPath, Symbol: symbol}, nil
	}
	if known {
		return ExplainRequest{Attribution: reason}, nil
	}
	return ExplainRequest{}, fmt.Errorf("explain: no culprit parsed from the reason; pass %s and %s, or %s for a seeding reason", spelling("package"), spelling("symbol"), spelling("witness"))
}

// Explain loads the accepted policy at dir and derives the chain for
// one culprit: the one entry both surfaces call, so a policy fault and
// the "explain: " error prefix have one shape.
func Explain(ctx context.Context, dir, pkgPath, symbol string) (gofresh.Chain, string, error) {
	pc, err := LoadCapture(ctx, dir)
	if err != nil {
		return gofresh.Chain{}, "", fmt.Errorf("explain: policy: %w", err)
	}
	chain, view, err := ExplainDynamicState(ctx, pc, pkgPath, symbol)
	if err != nil {
		// The freshness library prefixes its own explain errors; only
		// unprefixed causes (view construction) gain one.
		if strings.HasPrefix(err.Error(), "explain: ") {
			return gofresh.Chain{}, "", err
		}
		return gofresh.Chain{}, "", fmt.Errorf("explain: %w", err)
	}
	return chain, view, nil
}
