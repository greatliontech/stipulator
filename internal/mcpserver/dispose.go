package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/author"
)

type disposeIn struct {
	Kind        string `json:"kind"`
	Requirement string `json:"requirement,omitempty"`
	From        string `json:"from,omitempty"`
	Into        string `json:"into,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

func (s *Server) toolDispose(ctx context.Context, req *mcp.CallToolRequest, in disposeIn) (*mcp.CallToolResult, map[string]any, error) {
	var ups []author.Update
	var notes []string
	var err error
	switch in.Kind {
	case "editorial":
		var consented []string
		ups, consented, err = author.Editorial(s.fsys(), in.Requirement)
		for _, line := range consented {
			notes = append(notes, in.Requirement+": "+line)
		}
	case "retire":
		ups, err = author.Retire(s.fsys(), in.Requirement, in.Force)
	case "supersede":
		var from, into []string
		if from, err = splitIDs(in.From); err != nil {
			return nil, nil, fmt.Errorf("from: %w", err)
		}
		if into, err = splitIDs(in.Into); err != nil {
			return nil, nil, fmt.Errorf("into: %w", err)
		}
		ups, err = author.Supersede(s.fsys(), from, into, in.Force)
	default:
		return nil, nil, fmt.Errorf("unknown disposition kind %q (editorial, retire, supersede)", in.Kind)
	}
	if err != nil {
		return nil, nil, err
	}
	out, err := s.apply(ups)
	if err != nil {
		return nil, nil, err
	}
	out.Notes = append(out.Notes, notes...)
	return projected(out.result(), out.proto())
}
