package author

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
)

// Dispositions are operations, never records: each returns the file
// updates that are their only persistent effect — rewritten bindings,
// deleted gaps, and the tombstone registry. Nothing is logged; git holds
// history.

// Editorial re-pins a requirement's bindings to its current content hash:
// the author's claim that a spec edit preserved meaning. The claim is
// auditable in the diff, not machine-checkable.
func Editorial(fsys fs.FS, requirement string) ([]Update, error) {
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, err
	}
	hash := ""
	for _, r := range spec.GetRequirements() {
		if r.GetId() == requirement {
			hash = r.GetContentHash()
		}
	}
	if hash == "" {
		return nil, fmt.Errorf("requirement %s is not in the corpus", requirement)
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	var out []Update
	repinned := 0
	for _, bf := range store.Bindings {
		changed := false
		for _, b := range bf.Set.GetBindings() {
			if b.GetRequirementId() == requirement && b.GetContentHash() != hash {
				b.SetContentHash(hash)
				changed = true
				repinned++
			}
		}
		if changed {
			content, err := records.Render(bf)
			if err != nil {
				return nil, err
			}
			out = append(out, Update{Path: bf.Path, Content: content})
		}
	}
	if repinned == 0 {
		return nil, fmt.Errorf("no stale bindings for %s to re-pin", requirement)
	}
	sortUpdates(out)
	return out, nil
}

// Retire tombstones an identity already removed from the corpus and
// deletes its bindings and gap records. The corpus must compile with the
// tombstone in place — a lingering reference elsewhere refuses the
// retirement.
func Retire(fsys fs.FS, identity string, force bool) ([]Update, error) {
	return retire(fsys, []string{identity}, nil, force)
}

// Supersede implements split and merge: the sources, already removed from
// the corpus, are tombstoned; every successor must declare a supersedes
// edge to each source (edges are spec-owned); the sources' bindings are
// retargeted to every successor with content pins cleared — stale by
// contract, awaiting re-verification.
func Supersede(fsys fs.FS, sources, successors []string, force bool) ([]Update, error) {
	if len(successors) == 0 {
		return nil, fmt.Errorf("at least one successor is required")
	}
	return retire(fsys, sources, successors, force)
}

func retire(fsys fs.FS, identities, successors []string, force bool) ([]Update, error) {
	if len(identities) == 0 {
		return nil, fmt.Errorf("at least one identity is required")
	}
	for _, id := range identities {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("an identity is empty")
		}
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	for _, id := range identities {
		for _, t := range store.Tombstones {
			if strings.EqualFold(t, id) {
				return nil, fmt.Errorf("%s is already tombstoned", id)
			}
		}
	}
	// A typo must not tombstone silently: retiring an identity no record
	// names needs force.
	if !force {
		for _, id := range identities {
			named := false
			for _, bf := range store.Bindings {
				for _, b := range bf.Set.GetBindings() {
					if b.GetRequirementId() == id {
						named = true
					}
				}
			}
			for _, gf := range store.Gaps {
				if gf.Gap.GetRequirementId() == id {
					named = true
				}
			}
			if !named {
				return nil, fmt.Errorf("no record names %s; retiring an unrecorded identity requires force", id)
			}
		}
	}

	// Precheck when the base corpus compiles: an identity still declared
	// gets the actionable message. Mid-disposition corpora legitimately
	// fail to compile (successors' supersedes clauses await the
	// tombstone), so a failing base defers judgment to the overlay.
	if base, diags, err := compile.Compile(fsys); err == nil && len(diags) == 0 {
		for _, r := range base.GetRequirements() {
			for _, id := range identities {
				if r.GetId() == id {
					return nil, fmt.Errorf("%s is still declared in the corpus; remove it from the spec first", id)
				}
			}
		}
	}

	newRetired := append(append([]string{}, store.Tombstones...), identities...)
	sort.Strings(newRetired)
	tombstones := records.RenderTombstones(newRetired)

	// Validate the post-disposition corpus through an overlay: nothing may
	// still reference the identities, and successors must exist and
	// declare their supersedes edges.
	spec, diags, err := compile.Compile(overlayFS{FS: fsys, path: records.TombstonesPath, data: tombstones})
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, fmt.Errorf("cannot validate the retirement; corpus does not compile: %s%s", diags[0], moreSuffix(len(diags)-1))
	}
	inCorpus := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		inCorpus[r.GetId()] = true
	}
	for _, s := range successors {
		if !inCorpus[s] {
			return nil, fmt.Errorf("successor %s is not in the corpus", s)
		}
	}
	if len(successors) > 0 {
		declared := map[string]map[string]bool{}
		for _, e := range spec.GetEdges() {
			if e.GetKind() == stipulatorv1.EdgeKind_EDGE_KIND_SUPERSEDES && e.GetFrom().HasRequirementId() {
				from := e.GetFrom().GetRequirementId()
				if declared[from] == nil {
					declared[from] = map[string]bool{}
				}
				declared[from][e.GetTo().GetRequirementId()] = true
			}
		}
		for _, s := range successors {
			for _, id := range identities {
				if !declared[s][id] {
					return nil, fmt.Errorf("successor %s does not declare `supersedes %s` in its metadata; edges are spec-owned — add the clause first", s, id)
				}
			}
		}
	}

	retired := map[string]bool{}
	for _, id := range identities {
		retired[id] = true
	}

	// Collect the sources' bindings for retargeting, then remove them.
	var carried []*stipulatorv1.Binding
	updates, deletions, _, err := records.RemoveBindingsCollect(store, func(b *stipulatorv1.Binding) bool {
		return retired[b.GetRequirementId()]
	}, &carried)
	if err != nil {
		return nil, err
	}
	out := []Update{{Path: records.TombstonesPath, Content: tombstones}}
	touched := map[string][]byte{}
	for p, c := range updates {
		touched[p] = c
	}
	deleted := map[string]bool{}
	for _, p := range deletions {
		deleted[p] = true
	}

	// Retarget to successors: same symbol and role, content pin cleared.
	for _, succ := range successors {
		for _, old := range carried {
			nb := &stipulatorv1.Binding{}
			nb.SetRequirementId(succ)
			nb.SetBackend(old.GetBackend())
			nb.SetSymbol(old.GetSymbol())
			nb.SetRole(old.GetRole())
			if old.GetShapeHash() != "" {
				nb.SetShapeHash(old.GetShapeHash())
			}
			file := defaultBindingFile(succ)
			// Rebuild the store view incrementally so consecutive adds land
			// in the same file.
			if prior, ok := touched[file]; ok {
				sub, err := records.ParseBindingFile(file, prior)
				if err != nil {
					return nil, err
				}
				if bindingExists(sub, nb) {
					continue
				}
				content, err := records.AddBinding(storeOf(sub), file, nb)
				if err != nil {
					return nil, err
				}
				touched[file] = content
				continue
			}
			if existing := fileOf(store, file); existing != nil && bindingExists(*existing, nb) {
				continue
			}
			content, err := records.AddBinding(store, file, nb)
			if err != nil {
				return nil, err
			}
			touched[file] = content
		}
	}

	for p, c := range touched {
		out = append(out, Update{Path: p, Content: c})
		delete(deleted, p) // a retarget write to the same path wins
	}
	for p := range deleted {
		out = append(out, Update{Path: p, Content: nil})
	}
	// Delete gap records naming retired identities.
	for _, gf := range store.Gaps {
		if retired[gf.Gap.GetRequirementId()] {
			out = append(out, Update{Path: gf.Path, Content: nil})
		}
	}
	sortUpdates(out)
	return out, nil
}

func compileClean(fsys fs.FS) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, fmt.Errorf("corpus does not compile: %s%s", diags[0], moreSuffix(len(diags)-1))
	}
	return spec, nil
}

func bindingExists(bf records.BindingFile, nb *stipulatorv1.Binding) bool {
	for _, b := range bf.Set.GetBindings() {
		if b.GetRequirementId() == nb.GetRequirementId() && b.GetSymbol() == nb.GetSymbol() &&
			b.GetBackend() == nb.GetBackend() && b.GetRole() == nb.GetRole() {
			return true
		}
	}
	return false
}

func fileOf(store *records.Store, path string) *records.BindingFile {
	for i := range store.Bindings {
		if store.Bindings[i].Path == path {
			return &store.Bindings[i]
		}
	}
	return nil
}

func storeOf(bf records.BindingFile) *records.Store {
	return &records.Store{Bindings: []records.BindingFile{bf}}
}

// overlayFS serves one synthetic file over a base tree, for validating a
// disposition before any write happens.
type overlayFS struct {
	fs.FS
	path string
	data []byte
}

func (o overlayFS) ReadFile(name string) ([]byte, error) {
	if name == o.path {
		return o.data, nil
	}
	return fs.ReadFile(o.FS, name)
}
