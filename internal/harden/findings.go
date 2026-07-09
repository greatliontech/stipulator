package harden

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
)

// FindingsPath is where the mutation engine's findings document lives by
// convention — the engine's own default, at the tree root. stipulator only
// ever reads it (REQ-harden-findings): the engine owns the format and the
// writes; stipulator recovers requirement scoping from the labels it put on
// the targets export.
const FindingsPath = ".gomutant/findings.json"

// findingsVersion is the document version this reader understands; a
// version it does not understand is refused, while unknown fields within an
// understood version are ignored (the engine's own tolerance rule, mirrored:
// nothing stipulator does gates on a finding, so a dropped field only
// under-informs an advisory surface).
const findingsVersion = 1

// OraclePin is one test the engine ran against, pinned by the body hash it
// ran at — hash-compatible with stipulator's canon, so pin freshness is
// computable here without the engine.
type OraclePin struct {
	Symbol string `json:"symbol"`
	Hash   string `json:"hash"`
}

// EngineSurvivor is one mutant no oracle test noticed.
type EngineSurvivor struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
}

// EngineAttestation is one survivor disposition on the record.
type EngineAttestation struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
}

// EngineFinding is one measured symbol as the engine recorded it: the pins
// that scope the record and the measurement. Labels carry the requirement
// identifiers stipulator exported.
type EngineFinding struct {
	Symbol      string              `json:"symbol"`
	Labels      []string            `json:"labels"`
	BodyHash    string              `json:"bodyHash"`
	Oracle      []OraclePin         `json:"oracle"`
	OperatorSet string              `json:"operatorSet"`
	Budget      int                 `json:"budget"`
	Toolchain   string              `json:"toolchain"`
	Mutants     int                 `json:"mutants"`
	Killed      int                 `json:"killed"`
	Survivors   []EngineSurvivor    `json:"survivors"`
	Attested    []EngineAttestation `json:"attested"`
}

// LoadFindings reads the engine's findings document at path; a missing
// document is an empty set — nothing measured yet, every reminder entry
// reads as missing (REQ-harden-findings).
func LoadFindings(fsys fs.FS, path string) ([]EngineFinding, error) {
	data, err := fs.ReadFile(fsys, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		// Unreadable is not missing: an I/O or permission failure surfaces,
		// so the advisory surfaces degrade loudly rather than reporting
		// everything unmeasured.
		return nil, err
	}
	var doc struct {
		Version  int             `json:"version"`
		Findings []EngineFinding `json:"findings"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("harden: parse findings document %s: %w", path, err)
	}
	if doc.Version != findingsVersion {
		return nil, fmt.Errorf("harden: findings document version %d not understood (want %d)", doc.Version, findingsVersion)
	}
	return doc.Findings, nil
}
