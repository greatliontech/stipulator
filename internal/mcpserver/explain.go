package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/backends/golang"
)

type explainIn struct {
	Reason  string `json:"reason,omitempty"`
	Package string `json:"package,omitempty"`
	Symbol  string `json:"symbol,omitempty"`
}

type explainLink struct {
	Kind    string
	Package string
	Symbol  string
	Callee  string
	Clause  string
	Pos     string
}

type explainOut struct {
	Arm     string
	View    string
	Links   []explainLink
	Omitted int
}

// proto is the explain result's wire message.
func (e explainOut) proto() *stipulatorv1.ExplainResult {
	m := &stipulatorv1.ExplainResult{}
	m.SetArm(e.Arm)
	if e.View != "" {
		m.SetView(e.View)
	}
	links := make([]*stipulatorv1.ExplainLink, 0, len(e.Links))
	for _, l := range e.Links {
		lm := &stipulatorv1.ExplainLink{}
		lm.SetKind(l.Kind)
		lm.SetPackage(l.Package)
		for _, f := range []struct {
			v   string
			set func(string)
		}{{l.Symbol, lm.SetSymbol}, {l.Callee, lm.SetCallee}, {l.Clause, lm.SetClause}, {l.Pos, lm.SetPos}} {
			if f.v != "" {
				f.set(f.v)
			}
		}
		links = append(links, lm)
	}
	m.SetLinks(links)
	if e.Omitted > 0 {
		m.SetOmitted(int32(e.Omitted))
	}
	return m
}

func (s *Server) toolExplain(ctx context.Context, req *mcp.CallToolRequest, in explainIn) (*mcp.CallToolResult, map[string]any, error) {
	pkgPath, symbol, err := golang.ResolveCulprit(in.Reason, in.Package, in.Symbol, func(name string) string { return name })
	if err != nil {
		return nil, nil, err
	}
	ctx, prog := s.startProgress(ctx, req)
	prog.Phase(stipulatorv1.Phase_PHASE_DISCOVERY)
	chain, view, err := s.explain(ctx, pkgPath, symbol)
	if err != nil {
		return nil, nil, terminalToolError(prog, ctx, err)
	}
	prog.Terminal(stipulatorv1.TerminalCause_TERMINAL_CAUSE_COMPLETED)
	out := explainOut{Arm: chain.Arm, View: view, Omitted: chain.Omitted}
	for _, l := range chain.Links {
		out.Links = append(out.Links, explainLink{Kind: l.Kind, Package: l.Package, Symbol: l.Symbol, Callee: l.Callee, Clause: l.Clause, Pos: l.Pos})
	}
	digest := "explain: no chain - not a culprit in the policy views"
	if chain.Arm != "" {
		digest = fmt.Sprintf("explain: %s, %d links in the structured result; view: %s", chain.Arm, len(out.Links), view)
		if chain.Omitted > 0 {
			digest += fmt.Sprintf("; %d omitted", chain.Omitted)
		}
	}
	return projected(textOnly(digest), out.proto())
}
