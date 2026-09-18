package mcpserver

import (
	"errors"
	"fmt"
	"io/fs"
	pathpkg "path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/author"
)

// The export machinery the document-producing verbs share.

// exportTo validates a caller-named export path and writes the document
// under .stipulator/exports/ — the record-store home bounds every
// server write (REQ-mcp-writes-confined) — returning the location-only
// wire result.
func (s *Server) exportTo(exportPath string, doc []byte, what string) (*mcp.CallToolResult, map[string]any, error) {
	if err := validExportPath(exportPath); err != nil {
		return nil, nil, err
	}
	// The export overwrites its own prior: stamped from the read the
	// applier's precondition then checks (REQ-record-cas).
	up := author.Update{Path: exportPath, Content: doc}
	if prior, err := fs.ReadFile(s.fsys(), exportPath); err == nil {
		up.Prior = prior
	} else if errors.Is(err, fs.ErrNotExist) {
		up.PriorAbsent = true
	} else {
		return nil, nil, err
	}
	if _, err := s.applier.Apply([]author.Update{up}); err != nil {
		return nil, nil, err
	}
	m := &stipulatorv1.ExportResult{}
	m.SetExported(exportPath)
	m.SetBytes(int32(len(doc)))
	return projected(textOnly(fmt.Sprintf("%s: exported %d bytes to %s", what, len(doc), exportPath)), m)
}

// validExportPath refuses anything outside the export home. Tools with
// an expensive pass validate BEFORE running it: a typo must not cost a
// witness run only to be refused at write time.
func validExportPath(exportPath string) error {
	if exportPath == "" {
		return nil
	}
	clean := pathpkg.Clean(exportPath)
	if clean != exportPath || !strings.HasPrefix(clean, ".stipulator/exports/") || strings.Contains(clean, "..") {
		return fmt.Errorf("export_path must be a clean path under .stipulator/exports/ (the server writes nowhere else)")
	}
	return nil
}
