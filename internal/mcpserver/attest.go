package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/author"
)

type attestRequirementIn struct {
	Requirement string `json:"requirement"`
	Reason      string `json:"reason,omitempty"`
	Retract     bool   `json:"retract,omitempty"`
}

func (s *Server) toolAttestRequirement(ctx context.Context, req *mcp.CallToolRequest, in attestRequirementIn) (*mcp.CallToolResult, map[string]any, error) {
	if in.Retract {
		up, prior, err := author.RetractAttestation(s.fsys(), in.Requirement)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.apply([]author.Update{*up})
		if err != nil {
			return nil, nil, err
		}
		out.Notes = []string{"retracted judgment: " + prior.GetReason()}
		return projected(out.result(), out.proto())
	}
	up, prior, err := author.AttestRequirement(s.fsys(), in.Requirement, in.Reason)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.apply([]author.Update{*up})
	if err != nil {
		return nil, nil, err
	}
	if prior != nil {
		out.Notes = []string{"replaced judgment: " + prior.GetReason()}
	}
	return projected(out.result(), out.proto())
}
