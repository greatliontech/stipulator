package mcpserver

import (
	"github.com/greatliontech/stipulator/internal/bundle"
)

// The bundled-document rendering the spec-reading surfaces share.

func (s *Server) bundleMarkdown(commaIDs string) (string, error) {
	spec, err := s.compileFresh()
	if err != nil {
		return "", err
	}
	// The shared splitter: a JSON-array-encoded ids field must parse
	// here exactly as it does on every other ids-taking tool, not
	// mangle into one unknown identifier.
	ids, err := splitIDs(commaIDs)
	if err != nil {
		return "", err
	}
	b, err := bundle.Compute(spec, ids)
	if err != nil {
		return "", err
	}
	return bundle.Markdown(b, ids), nil
}
