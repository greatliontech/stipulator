package golang

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// pgoObservation is immutable after construction. The metadata query names
// only accepted import paths, under the compiling invocation's selection;
// validating it never replaces discovery's obligation set.
type pgoObservation struct {
	root     string
	pgo      string
	inputs   []string
	selected []listedPackage
	query    *NormalizedInvocation
}

// buildInputDrift records a comparison that demonstrated movement of an
// already observed compilation input. Read/analysis failures remain distinct.
type buildInputDrift struct {
	detail string
	cause  error
}

func (e *buildInputDrift) Error() string { return e.detail }
func (e *buildInputDrift) Unwrap() error { return e.cause }

func observePGO(ctx context.Context, root string, g *captureGroup) (*pgoObservation, error) {
	o := &pgoObservation{root: root, pgo: g.pgo, selected: pgoPackageFacts(g.selectedPackages)}
	if (g.pgo == "" || g.pgo == "auto") && len(o.selected) != 0 {
		var paths []string
		for _, p := range o.selected {
			paths = append(paths, p.ImportPath)
		}
		o.query = &NormalizedInvocation{
			Name: strings.Join(g.invs, ","), Dir: g.directory(root),
			Env: slices.Clone(g.witnessEnv), Tags: slices.Clone(g.tags), Race: g.race,
			ModuleMode: g.moduleMode, Packages: slices.Compact(paths), TelemetrySource: g.telemetrySource,
		}
		if err := o.validateSelection(ctx); err != nil {
			return nil, err
		}
	}
	var err error
	o.inputs, err = pgoBuildInputs(ctx, root, g.pgo, o.selected)
	return o, err
}

// pgoPackageFacts drops enumeration details that cannot select a profile and
// canonicalizes the remaining original-package facts for equality judgment.
func pgoPackageFacts(packages []listedPackage) []listedPackage {
	result := make([]listedPackage, 0, len(packages))
	for _, p := range packages {
		result = append(result, listedPackage{ImportPath: p.ImportPath, Name: p.Name, Dir: p.Dir})
	}
	slices.SortFunc(result, func(a, b listedPackage) int {
		if c := strings.Compare(a.ImportPath, b.ImportPath); c != 0 {
			return c
		}
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Dir, b.Dir)
	})
	return slices.CompactFunc(result, samePGOPackage)
}

func samePGOPackage(a, b listedPackage) bool {
	return a.ImportPath == b.ImportPath && a.Name == b.Name && a.Dir == b.Dir
}

func (o *pgoObservation) validateSelection(ctx context.Context) error {
	if o.query == nil {
		return ctx.Err()
	}
	current, err := listPackageMetadata(ctx, o.query)
	if err != nil {
		return fmt.Errorf("pgo selected package metadata: %w", err)
	}
	for _, pkg := range current {
		if pkg.Error != nil {
			return fmt.Errorf("pgo selected package metadata for %s: %s", pkg.ImportPath, pkg.Error.Err)
		}
		if pkg.Name == "" || pkg.Dir == "" {
			return fmt.Errorf("pgo selected package metadata incomplete for %s", pkg.ImportPath)
		}
	}
	if !slices.EqualFunc(o.selected, pgoPackageFacts(current), samePGOPackage) {
		return &buildInputDrift{detail: fmt.Sprintf("pgo selected package metadata changed for %s", strings.Join(o.query.Packages, ", "))}
	}
	return nil
}

func (o *pgoObservation) validate(ctx context.Context) error {
	if o == nil {
		return ctx.Err()
	}
	if err := o.validateSelection(ctx); err != nil {
		return err
	}
	current, err := pgoBuildInputs(ctx, o.root, o.pgo, o.selected)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &buildInputDrift{detail: err.Error(), cause: err}
		}
		return err
	}
	if !slices.Equal(current, o.inputs) {
		return &buildInputDrift{detail: fmt.Sprintf("pgo build inputs changed: opening %v; current %v", o.inputs, current)}
	}
	return nil
}

// buildPGO resolves a declared profile from the corpus root for every
// analysis or producing command, independently of its module directory.
func buildPGO(root, value string) string {
	if value == "" || value == "auto" || value == "off" {
		return value
	}
	return filepath.Join(root, filepath.FromSlash(value))
}

// pgoBuildInputs observes the profiles cmd/go selects for the original
// packages, before it constructs test binaries. Omission uses Go's default
// auto mode: only an original main package consumes its own default.pgo.
// Missing defaults are explicit inputs so their appearance moves the guard;
// a named profile must exist. Other stat/read failures refuse even though
// cmd/go skips a default on any stat error: uncertainty is not absence proof.
// These are opaque input values, not engine-watched files; a caller retaining
// an engine across execution must re-observe them before accepting its results.
func pgoBuildInputs(ctx context.Context, dir, pgo string, selected []listedPackage) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pgo == "off" {
		return nil, nil
	}
	optional := pgo == "" || pgo == "auto"
	var paths []string
	if optional {
		for _, pkg := range selected {
			if pkg.Name == "" || pkg.Dir == "" {
				return nil, fmt.Errorf("pgo auto: incomplete selected package metadata for %q", pkg.ImportPath)
			}
			if pkg.Name == "main" {
				paths = append(paths, filepath.Join(pkg.Dir, "default.pgo"))
			}
		}
	} else {
		paths = []string{buildPGO(dir, pgo)}
	}
	sort.Strings(paths)
	paths = slices.Compact(paths)
	var inputs []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil, fmt.Errorf("pgo profile %s: %w", path, err)
		}
		identity := fmt.Sprintf("pgo:%q=", filepath.ToSlash(rel))
		info, err := os.Stat(path)
		if optional && errors.Is(err, fs.ErrNotExist) {
			inputs = append(inputs, identity+"absent")
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("pgo profile %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("pgo profile %s: not a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("pgo profile %s: %w", path, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inputs = append(inputs, fmt.Sprintf("%s%x", identity, sha256.Sum256(data)))
	}
	return inputs, nil
}
