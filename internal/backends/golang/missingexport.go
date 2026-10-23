package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/greatliontech/go-x-tools/go/packages"
	"github.com/greatliontech/gofresh/gotool"
)

type missingExport struct {
	packagePath, file string
}

// missingExportError records an observation beside a failed typed load, not a
// diagnosis of who removed a cache entry or proof that it caused every error.
// packages.Error does not retain the importer's wrapped filesystem error.
type missingExportError struct {
	owner, directory, cache, toolchain, workspace, goflags string
	flags                                                  []string
	missing                                                []missingExport
	diagnostics                                            []packages.Error
}

func (e *missingExportError) Error() string {
	var observed, diagnostics []string
	for _, m := range e.missing {
		observed = append(observed, fmt.Sprintf("dependency %q export %q", m.packagePath, m.file))
	}
	for _, d := range e.diagnostics {
		diagnostics = append(diagnostics, loaderText(d))
	}
	return fmt.Sprintf("Go export-cache unavailable: %s absent when checked after package %q failed to load; typed resolution refused. Refresh exports with go list -export -a (including these dependencies), then start a new verification, using the same complete loader environment and build flags: directory=%q GOCACHE=%q GOTOOLCHAIN=%q GOWORK=%q GOFLAGS=%q flags=%q; loader: %s",
		strings.Join(observed, "; "), e.owner, e.directory, e.cache, e.toolchain, e.workspace, e.goflags, e.flags, strings.Join(diagnostics, "; "))
}

// loadTypedPackages preserves the caller's entire configuration. NeedExportFile
// retains metadata the typed loader already requests; it adds no listing. Only
// an erroring package with an incomplete, erroring dependency whose known export
// is absent pays for an environment snapshot to establish the effective cache.
// No old typed result or alternate selection is a recovery path.
func loadTypedPackages(cfg *packages.Config, patterns ...string) ([]*packages.Package, map[*packages.Package]*missingExportError, error) {
	selected := *cfg
	selected.Mode |= packages.NeedExportFile
	loaded, err := packages.Load(&selected, patterns...)
	if err != nil {
		return nil, nil, err
	}
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	reader := gotool.NewEnvReader(ownedRunner, cfg.Dir, cfg.Env)
	var failures map[*packages.Package]*missingExportError
	for _, pkg := range loaded {
		missing := missingDependencyExports(pkg)
		if len(missing) == 0 {
			continue
		}
		snapshot, err := reader.Snapshot(ctx)
		if err != nil {
			// An unavailable configuration cannot identify a disposable
			// cache artifact. The original package errors still refuse.
			continue
		}
		cache := snapshot.Value("GOCACHE")
		missing = slices.DeleteFunc(missing, func(m missingExport) bool {
			return !exportInCache(cache, m.file)
		})
		if len(missing) == 0 {
			continue
		}
		if failures == nil {
			failures = map[*packages.Package]*missingExportError{}
		}
		failures[pkg] = &missingExportError{
			owner: pkg.PkgPath, directory: cfg.Dir, cache: cache,
			toolchain: snapshot.Value("GOTOOLCHAIN"), workspace: snapshot.Value("GOWORK"), goflags: snapshot.Value("GOFLAGS"),
			flags: slices.Clone(cfg.BuildFlags), missing: missing, diagnostics: slices.Clone(pkg.Errors),
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return loaded, failures, nil
}

// missingDependencyExports uses the selected import graph and actual filesystem
// observations, never diagnostic wording. A healthy, complete dependency whose
// export later disappears does not attribute an unrelated source failure to the
// cache. The observation is advisory: no claim of exclusive causality follows.
func missingDependencyExports(pkg *packages.Package) []missingExport {
	if len(pkg.Errors) == 0 {
		return nil
	}
	var missing []missingExport
	seen := map[*packages.Package]bool{pkg: true}
	var visit func(*packages.Package)
	visit = func(dep *packages.Package) {
		if dep == nil || seen[dep] {
			return
		}
		seen[dep] = true
		// Only a failed import path can attribute the owner's failure.
		// Successfully loaded export data may retain metadata for a failed
		// transitive package that another root needed, without depending on
		// that package's export to establish this import's complete types.
		if len(dep.Errors) == 0 {
			return
		}
		if dep.Types != nil && !dep.Types.Complete() && filepath.IsAbs(dep.ExportFile) {
			if _, err := os.Stat(dep.ExportFile); errors.Is(err, os.ErrNotExist) {
				missing = append(missing, missingExport{dep.PkgPath, dep.ExportFile})
			}
		}
		for _, child := range dep.Imports {
			visit(child)
		}
	}
	for _, dep := range pkg.Imports {
		visit(dep)
	}
	slices.SortFunc(missing, func(a, b missingExport) int {
		if n := strings.Compare(a.packagePath, b.packagePath); n != 0 {
			return n
		}
		return strings.Compare(a.file, b.file)
	})
	return missing
}

func exportInCache(cache, export string) bool {
	if !filepath.IsAbs(cache) || !filepath.IsAbs(export) {
		return false
	}
	cache, err := filepath.EvalSymlinks(cache)
	if err != nil {
		return false
	}
	info, err := os.Stat(cache)
	if err != nil || !info.IsDir() {
		return false
	}
	// The export is absent, so resolve its deepest existing ancestor and
	// append only the absent suffix. An existing symlink (even a dangling
	// one) must resolve; skipping it could turn an escaping shard into an
	// apparently local path. The cache root may itself be a valid alias.
	var suffix []string
	for {
		_, err := os.Lstat(export)
		if err == nil {
			export, err = filepath.EvalSymlinks(export)
			if err != nil {
				return false
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		// Split rather than Dir: cleaning before resolving existing links
		// would change the meaning of a path containing link/.. . An absent
		// ancestor followed by . or .. cannot establish a physical path.
		parent, name := filepath.Split(export)
		if name == "" || name == "." || name == ".." {
			return false
		}
		suffix = append(suffix, name)
		if len(parent) > len(filepath.VolumeName(parent))+1 {
			parent = parent[:len(parent)-1]
		}
		export = parent
	}
	for _, name := range slices.Backward(suffix) {
		export = filepath.Join(export, name)
	}
	rel, err := filepath.Rel(cache, export)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}
