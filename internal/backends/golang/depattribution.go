package golang

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

// Load-failure attribution: a package that fails to load because an
// imported dependency does not provide what the tree references is a
// dependency-resolution state, not a defect of this tree's code — the
// committed pins resolve the import at a version or directory whose
// surface differs from what the source expects. Surfacing the raw
// loader error leaves the operator re-deriving that correlation by
// hand on every cross-repo staging round, so the classified message
// names the import, its providing module, and the committed directives
// that pin it (REQ-go-load-attribution) — directives, not "the"
// resolution: in a multi-member workspace minimal version selection
// chooses among them, a claim the committed text alone cannot make.
// Classification is best-effort over the loader's message shapes: an
// unrecognized shape, or a package whose own module cannot be
// identified, surfaces the loader's diagnostic unchanged, never a
// guessed attribution.

// undefinedSelectorRe matches the compiler's message for a reference
// to a name an imported package does not declare. Unanchored: the
// loader may deliver the message wrapped in compiler-output framing
// ("# pkg\n./file.go:9:18: undefined: dep.Missing"), so the shape is
// matched wherever it appears. The qualifier must be dotless, which
// excludes the local-name shapes ("undefined: x") and field messages
// ("x.y undefined (type ...)") the compiler spells differently.
var undefinedSelectorRe = regexp.MustCompile(`undefined: ([^.\s]+)\.(\S+)`)

// missingPackageRes match the go command's messages for an import no
// committed pin provides at all; the captured group is the package
// path.
var missingPackageRes = []*regexp.Regexp{
	regexp.MustCompile(`no required module provides package (\S+)`),
	regexp.MustCompile(`missing go\.sum entry for module providing package (\S+)`),
	regexp.MustCompile(`cannot find package "?([^"\s]+)"?`),
}

// pinTable is the committed dependency-pin surface of the tree: the
// workspace members' own module paths, each member go.mod's require
// and replace directives, and go.work's replace directives. Derived on
// demand from the committed files alone — the one source of pin facts
// — and only on load-error paths, so its cost never rides a healthy
// resolution.
type pinTable struct {
	members map[string]string // module path -> member dir
	// requires collects EVERY member's directive for a module: in a
	// multi-member workspace the toolchain selects among them by
	// minimal version selection, so naming any single directive as
	// "the" pin would assert a resolution the committed text alone
	// cannot prove — the attribution names them all.
	requires map[string][]string // module path -> "version (member go.mod)"
	// replaces holds unconditional replace directives — the one class
	// that genuinely supersedes every require for its module.
	replaces map[string]string // module path -> "replacement (source file)"
	// condReplaces holds version-scoped replace directives: each
	// applies at its old version only, so it renders BESIDE the
	// requires — suppressing the require that actually resolves the
	// import behind an inert conditional replace would name a directive
	// that does not apply while withholding the one that does.
	condReplaces map[string][]string
}

// loadPinTable parses the committed pin surface under root. Parse
// faults yield a partial table rather than an error: attribution is a
// diagnostic refinement, and a broken go.mod already fails the load
// itself with its own message.
func loadPinTable(root string) *pinTable {
	t := &pinTable{
		members:      map[string]string{},
		requires:     map[string][]string{},
		replaces:     map[string]string{},
		condReplaces: map[string][]string{},
	}
	members, err := workspaceMembers(root)
	if err != nil {
		members = []string{"."}
	}
	for _, m := range members {
		src := filepath.ToSlash(filepath.Join(m, "go.mod"))
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(m), "go.mod"))
		if err != nil {
			continue
		}
		f, err := modfile.Parse(src, raw, nil)
		if err != nil {
			continue
		}
		if f.Module != nil {
			t.members[f.Module.Mod.Path] = m
		}
		for _, r := range f.Require {
			t.requires[r.Mod.Path] = append(t.requires[r.Mod.Path], fmt.Sprintf("%s (%s require)", r.Mod.Version, src))
		}
		for _, r := range f.Replace {
			t.addReplace(r, src)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "go.work")); err == nil {
		if wf, err := modfile.ParseWork("go.work", raw, nil); err == nil {
			// go.work replaces override member replaces workspace-wide.
			for _, r := range wf.Replace {
				t.addReplace(r, "go.work")
			}
		}
	}
	return t
}

// addReplace files one replace directive: an unconditional one
// supersedes (last writer wins, go.work last), while a version-scoped
// one — applying at its old version only — accumulates to render
// beside the requires.
func (t *pinTable) addReplace(r *modfile.Replace, src string) {
	if r.Old.Version != "" {
		t.condReplaces[r.Old.Path] = append(t.condReplaces[r.Old.Path],
			fmt.Sprintf("replaced to %s (%s, applying at %s only)", replaceTarget(r.New), src, r.Old.Version))
		return
	}
	t.replaces[r.Old.Path] = fmt.Sprintf("replaced to %s (%s)", replaceTarget(r.New), src)
}

func replaceTarget(v module.Version) string {
	if v.Version == "" {
		return v.Path
	}
	return v.Path + "@" + v.Version
}

// pin describes the committed directives pinning importPath: the
// longest module-path prefix at a path boundary across the tree's
// replaces, members, and requires. An unconditional replace supersedes
// — the one directive class that resolves every version — while a
// member's working copy, the require directives (all of them, minimal
// version selection choosing among disagreeing members), and any
// version-scoped replaces render together, so no applying directive is
// withheld and no inert one asserted as the resolution. The second
// result is false when no committed directive speaks for the path.
func (t *pinTable) pin(importPath string) (string, bool) {
	best := ""
	consider := func(mod string) {
		if len(mod) > len(best) && moduleOwns(mod, importPath) {
			best = mod
		}
	}
	for mod := range t.requires {
		consider(mod)
	}
	for mod := range t.members {
		consider(mod)
	}
	for mod := range t.replaces {
		consider(mod)
	}
	for mod := range t.condReplaces {
		consider(mod)
	}
	if best == "" {
		return "", false
	}
	// The main clause: an all-versions replace supersedes the member
	// and require views entirely; otherwise the member's working copy,
	// then the requires. Version-scoped replaces compose with EVERY
	// main clause — the toolchain consults the exact-version directive
	// before the all-versions one, so at its version the scoped
	// directive is the one that resolves and withholding it would name
	// the directive that loses.
	var main string
	switch reqs := requiresBySemver(t.requires[best]); {
	case t.replaces[best] != "":
		main = t.replaces[best]
	case t.members[best] != "":
		main = fmt.Sprintf("at workspace member %q", t.members[best])
	case len(reqs) == 1:
		main = "at " + reqs[0]
	case len(reqs) > 1:
		main = fmt.Sprintf("pinned at %s — the toolchain selects among these by minimal version selection", strings.Join(reqs, " and "))
	}
	cond := condReplacesBySemver(t.condReplaces[best])
	switch {
	case main != "" && len(cond) > 0:
		return fmt.Sprintf("module %s %s, beside %s", best, main, strings.Join(cond, " and ")), true
	case main != "":
		return fmt.Sprintf("module %s %s", best, main), true
	default:
		return fmt.Sprintf("module %s %s, with no require directive beside it", best, strings.Join(cond, " and ")), true
	}
}

// moduleOwns reports mod owning path at a path boundary — the one
// prefix rule every owner question shares, so no two answers can
// diverge on nested member modules.
func moduleOwns(mod, path string) bool {
	return path == mod || strings.HasPrefix(path, mod+"/")
}

// condReplacesBySemver orders version-scoped replace descriptions by
// their applying-version's semantic precedence, tie-broken on the
// rendered string for determinism.
func condReplacesBySemver(cond []string) []string {
	out := append([]string(nil), cond...)
	version := func(s string) string {
		_, after, ok := strings.Cut(s, "applying at ")
		if !ok {
			return ""
		}
		v, _, _ := strings.Cut(after, " ")
		return v
	}
	sort.SliceStable(out, func(i, j int) bool {
		if c := semver.Compare(version(out[i]), version(out[j])); c != 0 {
			return c < 0
		}
		return out[i] < out[j]
	})
	return out
}

// requiresBySemver orders require descriptions by their version's
// semantic precedence — the order minimal version selection reasons in
// — never lexicographically, where v1.10.0 would sort before v1.9.0.
func requiresBySemver(reqs []string) []string {
	out := append([]string(nil), reqs...)
	sort.SliceStable(out, func(i, j int) bool {
		vi, _, _ := strings.Cut(out[i], " ")
		vj, _, _ := strings.Cut(out[j], " ")
		if c := semver.Compare(vi, vj); c != 0 {
			return c < 0
		}
		return out[i] < out[j]
	})
	return out
}

// memberModule reports whether pkgPath lives inside one of the tree's
// own member modules — an import of a member is still attributable
// (its pin is the working copy), but an error INSIDE a member package
// referencing another member's absent export attributes to that
// member's pin exactly as an external module's would. An external test
// package's "_test" path suffix folds to its base package, so a
// module-root xtest package still identifies its module.
func (t *pinTable) memberModule(pkgPath string) (string, bool) {
	// The untrimmed path is tried first: a member module whose own path
	// ends in "_test" must identify its root package before the fold
	// could mistake that package for an xtest of a shorter path. The
	// match is longest-wins over moduleOwns, the same rule pin applies
	// — a first-match walk over the map would answer nested member
	// modules by iteration order, and the same commit must yield the
	// same attribution on every run.
	for _, p := range []string{pkgPath, strings.TrimSuffix(pkgPath, "_test")} {
		best := ""
		for mod := range t.members {
			if len(mod) > len(best) && moduleOwns(mod, p) {
				best = mod
			}
		}
		if best != "" {
			return best, true
		}
	}
	return "", false
}

// loadErrorAttribution classifies a failed package's first
// dependency-rooted load error, returning the classified message and
// true, or "" and false when no error classifies — the caller then
// surfaces the loader's diagnostic unchanged.
func (b *Backend) loadErrorAttribution(pkg *packages.Package) (string, bool) {
	t := b.pins()
	// Fail closed on an unidentifiable own module: without knowing
	// which module the erroring package belongs to, an in-tree
	// reference cannot be told apart from a dependency one, and a
	// guessed attribution is forbidden — the loader's raw diagnostic
	// surfaces instead.
	ownModule, ok := t.memberModule(pkg.PkgPath)
	if !ok {
		return "", false
	}
	for _, e := range pkg.Errors {
		if imp, ok := dependencyImport(pkg, e, ownModule); ok {
			if desc, ok := t.pin(imp); ok {
				return fmt.Sprintf("dependency-resolution state: import %q is resolved by %s, whose surface does not satisfy this tree's reference — %s", imp, desc, loaderText(e)), true
			}
			return fmt.Sprintf("dependency-resolution state: import %q is resolved by no committed pin — %s", imp, loaderText(e)), true
		}
	}
	return "", false
}

// loaderText renders one loader error for an attribution message:
// position-prefixed when the loader supplied a real one, the
// compiler-output framing lines ("# pkg") dropped as redundant with
// the named package, and lines joined so the message stays
// single-line for the row-oriented renderers downstream.
func loaderText(e packages.Error) string {
	var parts []string
	for line := range strings.Lines(e.Msg) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		parts = append(parts, line)
	}
	msg := strings.Join(parts, "; ")
	if e.Pos != "" && e.Pos != "-" {
		return e.Pos + ": " + msg
	}
	return msg
}

// dependencyImport extracts the import path a load error is rooted in,
// when the error's shape identifies one outside the package's own
// module: an undefined selector whose qualifier is one of the erroring
// file's imports, or the go command's missing-package messages.
// ownModule is the erroring package's own member module — non-empty by
// the caller's fail-closed guard, which refuses to attribute at all
// when the module cannot be identified.
func dependencyImport(pkg *packages.Package, e packages.Error, ownModule string) (string, bool) {
	if m := undefinedSelectorRe.FindStringSubmatch(e.Msg); m != nil {
		if imp, ok := importForIdent(pkg, m[1]); ok {
			// A selector on the tree's own module is an in-tree defect:
			// its "pin" is the working copy itself, so attributing it
			// would misdirect the operator to a pin that does not exist.
			if !moduleOwns(ownModule, imp) {
				return imp, true
			}
		}
		return "", false
	}
	for _, re := range missingPackageRes {
		if m := re.FindStringSubmatch(e.Msg); m != nil {
			if imp := m[1]; !moduleOwns(ownModule, imp) {
				return imp, true
			}
			return "", false
		}
	}
	return "", false
}

// importForIdent resolves an identifier to the import path it names,
// over the package's whole import table — a named import binds its
// name, a plain one the imported package's name when the loader knows
// it, else the path's last element. The loader's wrapped compile
// errors carry only an in-message position relative to the module
// directory (their Pos field reads "-"), so rather than parse message
// framing the walk is package-wide and answers only when the
// identifier names exactly one distinct path — an ambiguous name falls
// back to the loader's raw diagnostic rather than a guessed
// attribution.
func importForIdent(pkg *packages.Package, ident string) (string, bool) {
	found := ""
	for _, f := range pkg.Syntax {
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			} else if dep := pkg.Imports[path]; dep != nil && dep.Name != "" {
				name = dep.Name
			} else {
				name = path[strings.LastIndex(path, "/")+1:]
			}
			if name != ident {
				continue
			}
			if found != "" && found != path {
				return "", false
			}
			found = path
		}
	}
	return found, found != ""
}
