package harden

import (
	"encoding/json"
	"fmt"
)

// exportVersion tags the targets export; the format is stipulator's
// contract with any mutation engine that consumes it (REQ-harden-export).
const exportVersion = 1

// exportEntry is one target on the wire: the implementation symbol, the
// witness union that vouches for it, and the requirement identifiers it
// implements — the engine treats the identifiers as opaque labels.
type exportEntry struct {
	Symbol       string   `json:"symbol"`
	Witnesses    []string `json:"witnesses,omitempty"`
	Requirements []string `json:"requirements,omitempty"`
}

type exportDocument struct {
	Version int           `json:"stipulatorTargets"`
	Targets []exportEntry `json:"targets"`
}

// ExportTargets renders the targets export (REQ-harden-export): every
// planned target, witness-less ones included — the engine decides what a
// target with no vouching test means; the export never silently narrows the
// bound surface.
func ExportTargets(targets []Target) ([]byte, error) {
	doc := exportDocument{Version: exportVersion, Targets: make([]exportEntry, 0, len(targets))}
	for _, t := range targets {
		if t.Symbol == "" {
			return nil, fmt.Errorf("harden: target with no symbol")
		}
		doc.Targets = append(doc.Targets, exportEntry{
			Symbol:       t.Symbol,
			Witnesses:    t.Witnesses,
			Requirements: t.Requirements,
		})
	}
	return json.MarshalIndent(doc, "", "  ")
}
