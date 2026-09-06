package author

import (
	"errors"
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

// ErrNothingStale marks an editorial re-pin that found nothing to
// re-pin: an error for dispose (a disposition that changed nothing was
// probably a mistake), a clean no-op for pin --req. The wrapping
// NothingStaleError says WHY, so the no-op line states a fact — "text
// unchanged" only when records consent to the current text, "no
// records" when none name the requirement, and the re-attest ceremony
// when the only stale consent is an attestation's, which the
// editorial re-pin never touches (REQ-pin-backfill).
var ErrNothingStale = errors.New("nothing stale to re-pin")

// NothingStaleError is ErrNothingStale with its reason: Note is the
// user-facing phrase the no-op surfaces render after the identifier.
type NothingStaleError struct {
	Requirement string
	Note        string
}

func (e *NothingStaleError) Error() string        { return e.Requirement + ": " + e.Note }
func (e *NothingStaleError) Is(target error) bool { return target == ErrNothingStale }
func (e *NothingStaleError) Unwrap() error        { return ErrNothingStale }

// NoOpNote is the phrase a no-op re-pin surfaces for err, when err is
// ErrNothingStale; empty otherwise.
func NoOpNote(err error) string {
	var nse *NothingStaleError
	if errors.As(err, &nse) {
		return nse.Note
	}
	if errors.Is(err, ErrNothingStale) {
		return "text unchanged; nothing to re-consent"
	}
	return ""
}

// Editorial re-pins a requirement's bindings and gap record to its
// current content hash: the author's claim that a spec edit preserved
// meaning. The claim is auditable in the diff, not machine-checkable.
// Every re-pinned clause claim is named with the clause it now
// denotes (consented is one line per claim): an ordinal follows its
// item's position, so the re-consent must see what the edit made it
// point at; a clause claim the edited text no longer resolves refuses
// the whole re-pin — consent to a dangling claim is no consent
// (REQ-evidence-clause-claim).
func Editorial(fsys fs.FS, requirement string) (ups []Update, consented []string, err error) {
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, nil, err
	}
	target, ok := records.ByID(spec)[requirement]
	if !ok {
		return nil, nil, fmt.Errorf("requirement %s is not in the corpus", requirement)
	}
	hash, source := target.GetContentHash(), target.GetSourceHash()
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	var out []Update
	repinned := 0
	// repin is the one named re-consent over a record's pins, whatever
	// kind carries them: a record already consenting to the current
	// text is left alone; a rehash under the named form is named too —
	// the operator asked to re-consent, and learns there was nothing to
	// consent to for this record (REQ-evidence-consent-current); an
	// UNSET pin is stamped like any other — the explicit per-identity
	// ceremony is a consent to the current text, needing no pre-field
	// grace.
	repin := func(name, content, src string, set func(content, source string)) bool {
		if content == hash {
			return false
		}
		if records.JudgeConsent(content, src, hash, source) == records.Rehash {
			consented = append(consented, fmt.Sprintf("%s rehashed — %s", name, records.RehashNote))
		}
		set(hash, source)
		repinned++
		return true
	}
	for _, bf := range store.Bindings {
		changed := false
		for _, b := range bf.Set.GetBindings() {
			if b.GetRequirementId() != requirement || b.GetContentHash() == hash {
				continue
			}
			clause, ok := records.ResolveClause(target, b)
			if !ok {
				return nil, nil, fmt.Errorf("binding %s names %s, which %s no longer declares — rebind against its current clauses or unbind it before re-consenting: stipulator unbind --req %s --symbol %s --clause %s", b.GetSymbol(), records.ClauseName(b), requirement, requirement, b.GetSymbol(), records.ClauseSpelling(b))
			}
			if clause != nil {
				consented = append(consented, fmt.Sprintf("%s now claims %s", b.GetSymbol(), records.ClauseHeading(clause)))
			}
			if repin(b.GetSymbol(), b.GetContentHash(), b.GetSourceHash(), func(c, s string) { b.SetContentHash(c); b.SetSourceHash(s) }) {
				changed = true
			}
		}
		if changed {
			content, err := records.Render(bf)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, Update{Path: bf.Path, Content: content})
		}
	}
	// The gap record's consent surface rides the same ceremony: a gap
	// pinned to the prior hash is exactly as stale as a binding's
	// (REQ-gap-consent).
	for _, gf := range store.Gaps {
		if gf.Gap.GetRequirementId() != requirement {
			continue
		}
		if repin("gap "+gf.Path, gf.Gap.GetContentHash(), gf.Gap.GetSourceHash(), func(c, s string) { gf.Gap.SetContentHash(c); gf.Gap.SetSourceHash(s) }) {
			content, err := records.RenderGapFile(gf)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, Update{Path: gf.Path, Content: content})
		}
	}
	if repinned == 0 {
		return nil, nil, &NothingStaleError{Requirement: requirement, Note: nothingStaleNote(store, requirement, hash, source)}
	}
	sortUpdates(out)
	sort.Strings(consented)
	StampPriors(store, out)
	return out, consented, nil
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
				return nil, fmt.Errorf("no record names %s; retiring an unrecorded identity requires --force (mcp: force)", id)
			}
		}
	}

	// Precheck when the base corpus compiles: an identity still declared
	// gets the actionable message. Mid-disposition corpora legitimately
	// fail to compile (successors' supersedes clauses await the
	// tombstone), so a failing base defers judgment to the overlay.
	if base, diags, err := compile.Compile(fsys); err == nil && len(compile.Errors(diags)) == 0 {
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
	// The overlay is a hypothetical corpus — the named sources already
	// tombstoned — so a remedy it computes names a call that cannot
	// work from the base; the refusal quotes its faults alone.
	if errs := compile.Errors(diags); len(errs) > 0 {
		return nil, fmt.Errorf("cannot validate the retirement; corpus does not compile: %s%s", errs[0], moreSuffix(len(errs)-1))
	}
	corpus := records.HashesOf(spec)
	for _, s := range successors {
		if !corpus.Known(s) {
			return nil, fmt.Errorf("successor %s is not in the corpus", s)
		}
	}
	declared := map[string]map[string]bool{}
	if len(successors) > 0 {
		for _, e := range spec.GetEdges() {
			if e.GetKind() == stipulatorv1.EdgeKind_EDGE_KIND_SUPERSEDES && e.GetFrom().HasRequirementId() {
				from := e.GetFrom().GetRequirementId()
				if declared[from] == nil {
					declared[from] = map[string]bool{}
				}
				declared[from][e.GetTo().GetRequirementId()] = true
			}
		}
		// The disposition follows the declared edges: every named
		// successor must declare at least one named source and every
		// named source must be declared by at least one named
		// successor, so a connected split-or-merge component — a chain
		// included — is one call, and a source's bindings retarget to
		// exactly the successors that declare it, never to one that
		// does not (REQ-change-split-merge).
		for _, s := range successors {
			any := false
			for _, id := range identities {
				any = any || declared[s][id]
			}
			if !any {
				return nil, fmt.Errorf("successor %s declares `supersedes` for none of %s in its metadata; edges are spec-owned — add the clause first, or leave %s out", s, strings.Join(identities, ", "), s)
			}
		}
		for _, id := range identities {
			any := false
			for _, s := range successors {
				any = any || declared[s][id]
			}
			if !any {
				return nil, fmt.Errorf("no named successor declares `supersedes %s` in its metadata; edges are spec-owned — add the clause to the successor that took it over, or name that successor", id)
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

	// Retarget along the declared edges: a source's bindings go to the
	// successors that declare it, same symbol and role, content pin
	// cleared. A retire (no successors) carries nothing.
	for _, succ := range successors {
		for _, old := range carried {
			if !declared[succ][old.GetRequirementId()] {
				continue
			}
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
	StampPriors(store, out)
	return out, nil
}

func compileClean(fsys fs.FS) (*stipulatorv1.Spec, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	if refusal := compile.Refusal(diags); refusal != "" {
		return nil, fmt.Errorf("corpus does not compile: %s", refusal)
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

// nothingStaleNote states why a named re-pin has nothing to do: the
// requirement's bindings and gap consent to the current text, or no
// record names it at all, or the only consent not holding is an
// attestation's — a judgment the editorial re-pin never rewrites, so
// the note names the re-attest ceremony instead of claiming the text
// unchanged.
func nothingStaleNote(store *records.Store, requirement, hash, source string) string {
	named := false
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetRequirementId() == requirement {
				named = true
			}
		}
	}
	for _, gf := range store.Gaps {
		if gf.Gap.GetRequirementId() == requirement {
			named = true
		}
	}
	for _, af := range store.Attestations {
		for _, a := range af.Set.GetAttestations() {
			if a.GetRequirementId() != requirement {
				continue
			}
			named = true
			if !records.JudgeConsent(a.GetContentHash(), a.GetSourceHash(), hash, source).Holds() {
				return "no binding or gap awaits re-consent; its attestation was vouched for different text and the editorial re-pin never rewrites a judgment — re-attest: stipulator attest requirement --req " + requirement
			}
		}
	}
	if !named {
		return "no records name it; nothing to re-consent"
	}
	return "text unchanged; nothing to re-consent"
}
