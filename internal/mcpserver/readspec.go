package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

type readSpecIn struct {
	Ids string `json:"ids"`
}

func (s *Server) toolReadSpec(ctx context.Context, req *mcp.CallToolRequest, in readSpecIn) (*mcp.CallToolResult, map[string]any, error) {
	md, err := s.bundleMarkdown(in.Ids)
	if err != nil {
		return nil, nil, err
	}
	m := &stipulatorv1.ReadSpecResult{}
	m.SetSpec(md)
	return projected(textOnly(fmt.Sprintf("bundle: %d bytes in the structured result", len(md))), m)
}
