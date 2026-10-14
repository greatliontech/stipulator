package golang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/module"
)

// symbolPackageCandidates enumerates the two possible package boundaries of
// a qualified object or receiver method. Candidates are questions for the Go
// listing, never declarations or permission to load the whole tree.
func symbolPackageCandidates(symbol string) []string {
	last := strings.LastIndexByte(symbol, '.')
	if last <= 0 || !token.IsIdentifier(symbol[last+1:]) {
		return nil
	}
	var out []string
	pkg := symbol[:last]
	if module.CheckImportPath(pkg) == nil {
		out = append(out, pkg)
	}
	before := strings.LastIndexByte(pkg, '.')
	if before > strings.LastIndexByte(pkg, '/') && before > 0 && token.IsIdentifier(pkg[before+1:]) {
		if candidate := pkg[:before]; module.CheckImportPath(candidate) == nil {
			out = append(out, candidate)
		}
	}
	return out
}

// declarationPackages asks only for package metadata, before source capture
// and typed loading. Relative queries cannot turn an absent local candidate
// into a dependency fetched to satisfy its import spelling.
func declarationPackages(ctx context.Context, dir string, sel buildSelection, symbols []string) (map[string]bool, error) {
	members, err := sel.members(dir)
	if err != nil {
		return nil, err
	}
	modules, err := memberModules(dir, members)
	if err != nil {
		return nil, err
	}
	base, err := goworkEnv(dir)
	if err != nil {
		return nil, err
	}
	env := selectionViewEnv(base, sel)
	seen := map[string]bool{}
	for _, symbol := range symbols {
		for _, candidate := range symbolPackageCandidates(symbol) {
			seen[candidate] = true
		}
	}
	var candidates []string
	for candidate := range seen {
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	owned := memberPatterns(modules, candidates)
	declared := map[string]bool{}
	for _, member := range members {
		paths := owned[member]
		if len(paths) == 0 {
			continue
		}
		args := []string{"-e", "-json=ImportPath,Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,InvalidGoFiles,Error"}
		args = append(args, selectionViewFlags(sel)...)
		expected := map[string]string{}
		for _, path := range paths {
			rel := "."
			if path != modules[member] {
				rel = "./" + strings.TrimPrefix(path, modules[member]+"/")
			}
			args = append(args, rel)
			expected[rel], expected[path] = path, path
		}
		data, err := ownedRunner.List(ctx, filepath.Join(dir, member), env, args...)
		if err != nil {
			return nil, err
		}
		found, err := readDeclarationPackages(ctx, data, expected)
		if err != nil {
			return nil, err
		}
		for path := range found {
			declared[path] = true
		}
	}
	return declared, nil
}

// readDeclarationPackages requires one answer for every queried package.
// An omitted or malformed row cannot certify absence and select a shorter
// dotted name. Only explicit Go absence answers do so.
func readDeclarationPackages(ctx context.Context, data []byte, expected map[string]string) (map[string]bool, error) {
	pending := map[string]bool{}
	for _, path := range expected {
		pending[path] = true
	}
	declared := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var pkg struct {
			ImportPath, Dir                                              string
			GoFiles, CgoFiles, TestGoFiles, XTestGoFiles, InvalidGoFiles []string
			Error                                                        *listError
		}
		if err := dec.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("resolution package inventory: %w", err)
		}
		path, known := expected[pkg.ImportPath]
		if !known || !pending[path] {
			return nil, fmt.Errorf("resolution package inventory: unexpected or repeated package %q", pkg.ImportPath)
		}
		delete(pending, path)
		if pkg.Dir != "" && pkg.ImportPath == path && len(pkg.GoFiles)+len(pkg.CgoFiles)+len(pkg.TestGoFiles)+len(pkg.XTestGoFiles)+len(pkg.InvalidGoFiles) > 0 {
			declared[path] = true
			continue
		}
		if pkg.Error != nil {
			why := pkg.Error.Err
			if explicitPackageAbsence(why) {
				continue
			}
			return nil, fmt.Errorf("resolution package inventory: %s", why)
		}
		return nil, fmt.Errorf("resolution package inventory: package %q has no declaration or absence answer", pkg.ImportPath)
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("resolution package inventory: missing %d package answers", len(pending))
	}
	return declared, nil
}

// explicitPackageAbsence is shared by metadata and typed-root admission:
// a fileless error proves absence only when Go names an absent package,
// rather than a read, permission, or tool failure.
func explicitPackageAbsence(why string) bool {
	return strings.HasPrefix(why, "no Go files in ") ||
		strings.HasPrefix(why, "build constraints exclude all Go files in ") ||
		strings.HasPrefix(why, unprovidedPackagePrefix) ||
		(strings.HasPrefix(why, unfoundPackagePrefix) && strings.HasSuffix(why, vendorLookupDisabledSuffix)) ||
		(strings.HasPrefix(why, "main module (") && strings.Contains(why, ") does not contain package ")) ||
		strings.HasSuffix(why, ": directory not found")
}

func (s *Served) inventoryPackages(symbols []string) {
	s.inventory = map[string]map[string]bool{}
	keys := make([]string, 0, len(s.sels))
	for _, sel := range s.sels {
		keys = append(keys, sel.key())
	}
	sort.Strings(keys)
	for _, key := range keys {
		sel, _ := s.selection(key)
		pkgs, err := declarationPackages(s.ctx, s.dir, sel, symbols)
		if err != nil {
			s.degraded = append(s.degraded, fmt.Sprintf("selection %q: package inventory: %v", key, err))
			continue
		}
		s.inventory[key] = pkgs
	}
}

// inventoryComplete derives readiness from the successful per-selection
// entries. The intentionally unscoped form has no requested inventory.
func (s *Served) inventoryComplete() bool {
	return s.set == nil || len(s.inventory) == len(s.sels)
}

// packageOf is the longest declared package prefix across the selected
// views. A missing inventory never authorizes a guessed capture.
func (s *Served) packageOf(symbol string) (string, bool) {
	for _, candidate := range symbolPackageCandidates(symbol) {
		for _, pkgs := range s.inventory {
			if pkgs[candidate] {
				return candidate, true
			}
		}
	}
	// An empty-set serving form resolves the whole tree but has no
	// opening subjects. Its later account may name the typed answer;
	// the absent opening capture still prevents publication.
	if s.set == nil {
		if a := s.answers[symbol]; a.pkg != "" {
			return a.pkg, true
		}
	}
	return "", false
}
