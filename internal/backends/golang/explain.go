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

// ResolveCulprit settles what a surface's explain request names: the
// package and symbol when both are given, else the culprit parsed from
// the reason. A lone package or symbol is refused — the caller typed it
// for a reason — and so is a reason no culprit parses from. spelling
// renders the argument names in the refusals ("--package" on the CLI,
// "package" on the MCP), so both surfaces share one contract.
func ResolveCulprit(reason, pkgPath, symbol string, spelling func(string) string) (string, string, error) {
	if (pkgPath == "") != (symbol == "") {
		return "", "", fmt.Errorf("explain: %s and %s travel together", spelling("package"), spelling("symbol"))
	}
	if pkgPath != "" {
		return pkgPath, symbol, nil
	}
	if reason == "" {
		return "", "", fmt.Errorf("explain: pass %s to parse, or %s and %s", spelling("reason"), spelling("package"), spelling("symbol"))
	}
	pkgPath, symbol, ok := CulpritFromReason(reason)
	if !ok {
		return "", "", fmt.Errorf("explain: no culprit parsed from the reason; pass %s and %s", spelling("package"), spelling("symbol"))
	}
	return pkgPath, symbol, nil
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
