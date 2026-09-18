package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/greatliontech/stipulator/internal/records"
)

// --- resources ---

func (s *Server) readResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	switch {
	case strings.HasPrefix(uri, "stipulator://req/"):
		id := strings.TrimPrefix(uri, "stipulator://req/")
		spec, err := s.compileFresh()
		if err != nil {
			return nil, err
		}
		if r, ok := records.ByID(spec)[id]; ok {
			md := fmt.Sprintf("%s\n\n> id: %s | kind: %s | keyword: %s | content_hash: %s\n",
				r.GetSource(), r.GetId(),
				strings.ToLower(strings.TrimPrefix(r.GetKind().String(), "CLAUSE_KIND_")),
				strings.TrimPrefix(r.GetKeyword().String(), "KEYWORD_"),
				r.GetContentHash())
			return textResource(uri, "text/markdown", md), nil
		}
		return nil, mcp.ResourceNotFoundError(uri)
	case strings.HasPrefix(uri, "stipulator://term/"):
		name := strings.TrimPrefix(uri, "stipulator://term/")
		spec, err := s.compileFresh()
		if err != nil {
			return nil, err
		}
		for _, t := range spec.GetTerms() {
			if strings.EqualFold(t.GetName(), name) {
				return textResource(uri, "text/markdown", t.GetSource()+"\n"), nil
			}
		}
		return nil, mcp.ResourceNotFoundError(uri)
	case strings.HasPrefix(uri, "stipulator://bundle/"):
		ids := strings.TrimPrefix(uri, "stipulator://bundle/")
		md, err := s.bundleMarkdown(ids)
		if err != nil {
			return nil, err
		}
		return textResource(uri, "text/markdown", md), nil
	}
	return nil, mcp.ResourceNotFoundError(uri)
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: mime, Text: text,
	}}}
}
