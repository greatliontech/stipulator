package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"github.com/greatliontech/gofresh"
	"golang.org/x/tools/go/packages"
)

// The arms a witness's seeding derivation answers with: the bound body
// drives a runner directly (in the resolved view or another), reaches
// one through in-module helpers, or is refused — the walk met a call
// resolving to no declaration or a declaration it could not read —
// else nothing seeds it and serving consults the class alone.
const (
	ArmSeedingDirect  = "seeding: direct"
	ArmSeedingThrough = "seeding: through helpers"
	ArmSeedingRefused = "seeding: refused"
	ArmNotSeeded      = "not seeded"
)

// The link kinds of a seeding chain, beside the freshness library's
// dynamic-state kinds: the witness at its declaration, a call of a
// helper at its site (the callee named), the driving call at its site
// (the clause naming the driver), the refusing call at its site (the
// clause the refusal).
const (
	LinkWitness = "witness"
	LinkCall    = "call"
	LinkDriver  = "driver"
	LinkRefusal = "refusal"
)

// witnessChainBound caps a seeding chain's links as the freshness
// library caps its own (REQ-mcp-explain); the remainder is counted,
// never silent, and the deciding link — the driver or the refusal — is
// never the one dropped.
const witnessChainBound = 24

// explainWitness derives a witness's seeding chain: the classification
// and serving's own walk, rendered as the links serving decided over
// (REQ-mcp-explain). The view is the selection the resolved
// declaration was read under, or the other view whose body drove a
// runner directly.
func (b *Backend) explainWitness(symbol string) (gofresh.Chain, string, error) {
	fd, pkg, err := b.funcDecl(symbol)
	if err != nil {
		return gofresh.Chain{}, "", fmt.Errorf("witness %s: %w", symbol, err)
	}
	sel := b.selectionOf(pkg)
	witness := gofresh.ChainLink{Kind: LinkWitness, Package: pkg.PkgPath, Symbol: declName(fd, pkg), Pos: b.site(pkg, fd.Name.Pos())}
	if fd.Body == nil || !runnableWitness(fd, pkg) {
		return boundChain(ArmNotSeeded+" (not a runnable test witness)", []gofresh.ChainLink{witness}), viewLabel(sel), nil
	}
	// Serving's one answer: the resolved body's own driver first, then
	// the walk and the other views.
	s := b.seededInAnyView(symbol, sel, fd, pkg)
	if s.witnessSite != "" {
		// Another view answered: the witness at its declaration there.
		witness.Pos = s.witnessSite
	}
	switch {
	case s.direct:
		driver := gofresh.ChainLink{Kind: LinkDriver, Package: pkg.PkgPath, Symbol: declName(fd, pkg), Clause: driverClause, Pos: s.directSite}
		return boundChain(ArmSeedingDirect, []gofresh.ChainLink{witness, driver}), viewLabel(s.sel), nil
	case s.path.via != "":
		links := append([]gofresh.ChainLink{witness}, hopLinks(s.path.hops)...)
		last := s.path.hops[len(s.path.hops)-1].callee
		links = append(links, gofresh.ChainLink{Kind: LinkDriver, Package: pkgPathOf(last), Symbol: funcName(last), Clause: driverClause, Pos: s.path.driverSite})
		return boundChain(ArmSeedingThrough, links), viewLabel(s.sel), nil
	case s.path.refusal != "":
		// The refusal link names the body holding the refusing call —
		// the bound body, or the helper whose call resolved to no
		// declaration or whose callee could not be read — at that
		// call's site, after the hops into it.
		links := append([]gofresh.ChainLink{witness}, hopLinks(s.path.refusalHops)...)
		at := gofresh.ChainLink{Kind: LinkRefusal, Package: pkg.PkgPath, Symbol: declName(fd, pkg), Clause: s.path.refusal, Pos: s.path.refusalSite}
		if in := s.path.refusalIn; in != nil {
			at.Package, at.Symbol = pkgPathOf(in), funcName(in)
		}
		return boundChain(ArmSeedingRefused, append(links, at)), viewLabel(s.sel), nil
	}
	class := b.classifyWitness(symbol).class
	return boundChain(ArmNotSeeded+" ("+strings.ToLower(classWire(class))+")", []gofresh.ChainLink{witness}), viewLabel(sel), nil
}

// driverClause names what a driver link's call is: a run-time-seeded
// property runner's check driver.
const driverClause = "run-time-seeded property driver"

// hopLinks renders the walk's hops: each a call link from the caller
// (the bound body when nil) to the callee at the call's site.
func hopLinks(hops []seedingHop) []gofresh.ChainLink {
	links := make([]gofresh.ChainLink, 0, len(hops))
	for _, h := range hops {
		l := gofresh.ChainLink{Kind: LinkCall, Callee: h.callee.FullName(), Pos: h.site}
		if h.caller != nil {
			l.Package, l.Symbol = pkgPathOf(h.caller), funcName(h.caller)
		}
		links = append(links, l)
	}
	return links
}

// boundChain caps the links at witnessChainBound keeping the first and
// the last — the witness and the deciding link — and counts the rest.
func boundChain(arm string, links []gofresh.ChainLink) gofresh.Chain {
	c := gofresh.Chain{Arm: arm, Links: links}
	if n := len(links); n > witnessChainBound {
		kept := append(append([]gofresh.ChainLink{}, links[:witnessChainBound-1]...), links[n-1])
		c.Links, c.Omitted = kept, n-witnessChainBound
	}
	return c
}

// declName spells a declaration as a chain symbol through its object —
// "F" or "(T).M" — falling back to the bare name where the type
// information declares no object.
func declName(fd *ast.FuncDecl, pkg *packages.Package) string {
	if fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func); ok {
		return funcName(fn)
	}
	return fd.Name.Name
}

// funcName spells a function object as a chain symbol — "F" or
// "(T).M" — its package carried apart.
func funcName(fn *types.Func) string {
	full := fn.FullName()
	if p := fn.Pkg(); p != nil {
		full = strings.Replace(full, p.Path()+".", "", 1)
	}
	return full
}

// pkgPathOf names a function object's package, empty for a universe
// function.
func pkgPathOf(fn *types.Func) string {
	if p := fn.Pkg(); p != nil {
		return p.Path()
	}
	return ""
}

// ExplainWitness loads the policy's whole tree at dir and derives one
// witness's seeding chain through the owned resolver child — the one
// route a typed load takes — naming the view that answered
// (REQ-mcp-explain).
func ExplainWitness(ctx context.Context, dir, symbol string) (gofresh.Chain, string, error) {
	s, err := NewWholeTree(ctx, dir)
	if err != nil {
		return gofresh.Chain{}, "", fmt.Errorf("explain: policy: %w", err)
	}
	defer s.Close()
	chain, view, err := s.ExplainWitness(symbol)
	if err != nil {
		return gofresh.Chain{}, "", fmt.Errorf("explain: %w", err)
	}
	return chain, view, nil
}
