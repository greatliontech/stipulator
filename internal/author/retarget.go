package author

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// The symbol retarget: the bindings' rewrite, the enforcement pointers'
// rewrite, and the re-consent the rewrite owes.

// RetargetRow reports one rewritten binding identity.
type RetargetRow struct {
	Requirement string
	Old, New    string
	Role        stipulatorv1.BindingRole
}

// PointerRow is one enforcement pointer a retarget rewrites in a spec
// document: the requirement whose text names it, the document, and the
// member names before and after (REQ-change-enforcement-pointers).
type PointerRow struct {
	Requirement, Document string
	Old, New              string
}

// PointerClause is the faces' one spelling of a retarget's pointer
// count: " and N pointer(s)" when any moved, nothing otherwise — a
// rename that moved no pointer reads as it always did.
func (r *RetargetResult) PointerClause() string {
	if len(r.Pointers) == 0 {
		return ""
	}
	return fmt.Sprintf(" and %d pointer(s)", len(r.Pointers))
}

// RetargetResult is a symbol retarget's whole answer: the store updates
// (binding symbols rewritten, the affected requirements' content
// re-pinned) and the document updates (their pointers rewritten), with
// the rows naming each identity moved — a pointer row is an effected
// change, never an intent — and the consent lines the re-pin named.
type RetargetResult struct {
	Updates   []Update
	Rows      []RetargetRow
	Pointers  []PointerRow
	Consented []string
}

// Retarget rewrites stored binding symbols for one backend under an
// exact old-prefix-to-new-prefix mapping — the module-rename repair
// (REQ-change-retarget). A symbol matches only at a path or member
// boundary, so a prefix never captures a sibling that merely shares
// characters. All-or-nothing: every replacement must resolve through
// the backend (shape pins re-derive from those resolutions; content
// pins ride unchanged — the requirement text did not move), and a
// post-rewrite store carrying two claims of one identity — requirement,
// backend, symbol, role, and resolved clause, whether or not the
// rewrite touches either — refuses the whole batch, exactly as
// verification's hygiene names a duplicate. The returned rows report
// every old-to-new identity; callers preview by discarding the updates.
//
// The operation also rewrites the enforcement pointers the rewrite
// moves: for every rewritten binding whose member name changes, each
// pointer in that binding's requirement naming the old member is
// rewritten to the new one in the document — the one edit the tool
// makes to a spec document — and the requirement's content is re-pinned
// by the same operation over the rewritten corpus (its own mechanical
// edit is its own consent); a requirement whose raw source is not
// unique in its document refuses the whole retarget, since the rewrite
// could not be placed (REQ-change-enforcement-pointers).
func Retarget(fsys fs.FS, backends map[string]verify.Backend, backend, oldPrefix, newPrefix string) (*RetargetResult, error) {
	ups, rows, spec, err := retargetBindings(fsys, backends, backend, oldPrefix, newPrefix)
	if err != nil {
		return nil, err
	}
	res := &RetargetResult{Updates: ups, Rows: rows}
	// The corpus is consulted only when a member name moves: a prefix
	// move that keeps every member touches no pointer and needs no
	// compiling corpus — retarget stays a repair verb.
	moved := false
	for _, row := range rows {
		if records.SymbolMember(row.Old) != records.SymbolMember(row.New) {
			moved = true
		}
	}
	if !moved {
		return res, nil
	}
	// One corpus per operation: the collision check may have compiled
	// it already for an alias pair.
	if spec == nil {
		if spec, err = compileClean(fsys); err != nil {
			return nil, err
		}
	}
	byID := records.ByID(spec)
	// The pointer rewrites, grouped per document so one document is one
	// update: the requirement's raw source is located in the document
	// bytes and every backticked old member inside it is renamed.
	type docEdit struct {
		orig, cur []byte
	}
	docs := map[string]*docEdit{}
	affected := map[string]bool{}
	seenPointer := map[PointerRow]bool{}
	placed := map[[2]string]bool{}
	for _, row := range rows {
		oldMember, newMember := records.SymbolMember(row.Old), records.SymbolMember(row.New)
		if oldMember == newMember {
			continue
		}
		// Two bindings of one requirement on one symbol (two roles)
		// name one pointer rewrite: placed once, never re-sought in
		// the already rewritten source.
		if placed[[2]string{row.Requirement, oldMember}] {
			continue
		}
		placed[[2]string{row.Requirement, oldMember}] = true
		req, ok := byID[row.Requirement]
		if !ok {
			continue
		}
		// The spans to rewrite, from the one grammar: the extractor's
		// pointers naming the old member, right to left so earlier
		// offsets stay valid.
		var spans []*stipulatorv1.EnforcementPointer
		for _, p := range req.GetEnforcementPointers() {
			if p.GetName() == oldMember {
				spans = append(spans, p)
			}
		}
		if len(spans) == 0 {
			continue
		}
		path := req.GetLocation().GetDocument()
		d, ok := docs[path]
		if !ok {
			raw, err := fs.ReadFile(fsys, path)
			if err != nil {
				return nil, fmt.Errorf("reading %s to rewrite its pointers: %w", path, err)
			}
			d = &docEdit{orig: raw, cur: raw}
			docs[path] = d
		}
		src := []byte(req.GetSource())
		if n := bytes.Count(d.cur, src); n != 1 {
			return nil, fmt.Errorf("requirement %s's source occurs %d times in %s; its pointer rewrite cannot be placed, so the whole retarget is refused", row.Requirement, n, path)
		}
		start := bytes.Index(d.cur, src)
		rewritten := append([]byte(nil), src...)
		for k := len(spans) - 1; k >= 0; k-- {
			a, b := int(spans[k].GetStart()), int(spans[k].GetEnd())
			if a < 0 || b > len(rewritten) || a > b || string(rewritten[a:b]) != oldMember {
				return nil, fmt.Errorf("requirement %s's pointer `%s` is not at its recorded span in %s; the corpus and its IR disagree, so the whole retarget is refused", row.Requirement, oldMember, path)
			}
			rewritten = append(append(append([]byte(nil), rewritten[:a]...), newMember...), rewritten[b:]...)
		}
		d.cur = append(append(append([]byte(nil), d.cur[:start]...), rewritten...), d.cur[start+len(src):]...)
		affected[row.Requirement] = true
		pr := PointerRow{Requirement: row.Requirement, Document: path, Old: oldMember, New: newMember}
		if !seenPointer[pr] {
			seenPointer[pr] = true
			res.Pointers = append(res.Pointers, pr)
		}
	}
	if len(docs) == 0 {
		return res, nil
	}
	// The re-consent runs over the rewritten corpus AND the rewritten
	// store — an overlay of every update so far — so the re-pinned
	// binding files carry the new symbols too; the compare-and-swap
	// priors are then stamped from the store as read from disk.
	overlay := overlayFS{FS: fsys, files: map[string][]byte{}}
	for path, d := range docs {
		overlay.files[path] = d.cur
		res.Updates = append(res.Updates, Update{Path: path, Content: d.cur, Prior: d.orig, Document: true})
	}
	for _, up := range ups {
		overlay.files[up.Path] = up.Content
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	var ids []string
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	byPath := map[string]int{}
	for i, up := range res.Updates {
		byPath[up.Path] = i
	}
	merge := func(repins []Update) {
		StampPriors(store, repins)
		for _, rp := range repins {
			overlay.files[rp.Path] = rp.Content
			if i, ok := byPath[rp.Path]; ok {
				res.Updates[i] = rp
				continue
			}
			byPath[rp.Path] = len(res.Updates)
			res.Updates = append(res.Updates, rp)
		}
	}
	for _, id := range ids {
		repins, consented, err := Editorial(overlay, id)
		if err != nil {
			return nil, fmt.Errorf("re-pinning %s after its pointer rewrite: %w", id, err)
		}
		res.Consented = append(res.Consented, consented...)
		merge(repins)
		// The editorial ceremony leaves attestations to a human's
		// re-attest; this edit is the tool's own and moved no judgment,
		// so the attestation pins follow too — a stale attestation would
		// read the rewritten requirement red.
		repins, notes, err := repinAttestations(overlay, id)
		if err != nil {
			return nil, fmt.Errorf("re-pinning %s's attestations after its pointer rewrite: %w", id, err)
		}
		res.Consented = append(res.Consented, notes...)
		merge(repins)
	}
	return res, nil
}

// repinAttestations re-pins the requirement's attestations to the
// corpus as the overlay compiles it, naming each re-pin as the
// editorial ceremony names a binding's (REQ-evidence-consent-current).
func repinAttestations(fsys fs.FS, requirement string) ([]Update, []string, error) {
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, nil, err
	}
	target, ok := records.ByID(spec)[requirement]
	if !ok {
		return nil, nil, fmt.Errorf("requirement %s is not in the corpus", requirement)
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	var out []Update
	var notes []string
	for _, af := range store.Attestations {
		changed := false
		for _, a := range af.Set.GetAttestations() {
			if a.GetRequirementId() != requirement || a.GetContentHash() == target.GetContentHash() {
				continue
			}
			if records.JudgeConsent(a.GetContentHash(), a.GetSourceHash(), target.GetContentHash(), target.GetSourceHash()) == records.Rehash {
				notes = append(notes, fmt.Sprintf("attestation of %s rehashed — %s", requirement, records.RehashNote))
			} else {
				notes = append(notes, fmt.Sprintf("attestation of %s re-pinned to the rewritten text", requirement))
			}
			a.SetContentHash(target.GetContentHash())
			a.SetSourceHash(target.GetSourceHash())
			changed = true
		}
		if changed {
			content, err := records.RenderAttestationFile(af)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, Update{Path: af.Path, Content: content})
		}
	}
	return out, notes, nil
}

// retargetBindings is the binding-symbol half of a retarget; the spec
// it returns is the corpus it compiled for the collision check, nil
// when the check needed none.
func retargetBindings(fsys fs.FS, backends map[string]verify.Backend, backend, oldPrefix, newPrefix string) ([]Update, []RetargetRow, *stipulatorv1.Spec, error) {
	if backend == "" || oldPrefix == "" || newPrefix == "" {
		return nil, nil, nil, fmt.Errorf("a backend, an old prefix, and a new prefix are required")
	}
	if !KnownBackends[backend] {
		return nil, nil, nil, fmt.Errorf("unknown backend %q (go, proto)", backend)
	}
	if oldPrefix == newPrefix {
		return nil, nil, nil, fmt.Errorf("old and new prefixes are identical; nothing to retarget")
	}
	be, loaded := backends[backend]
	if !loaded {
		return nil, nil, nil, fmt.Errorf("no %s backend is loaded to resolve replacements; a retarget that cannot validate its rewrites is refused, not recorded", backend)
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, nil, err
	}

	// A prefix matches at a boundary only: the next rune after it is a
	// path separator or the member dot, never a bare character run.
	matches := func(symbol string) bool {
		if !strings.HasPrefix(symbol, oldPrefix) {
			return false
		}
		if len(symbol) == len(oldPrefix) {
			// A full-symbol match is the degenerate member boundary —
			// the single-symbol rename repair (REQ-change-retarget).
			return true
		}
		return symbol[len(oldPrefix)] == '/' || symbol[len(oldPrefix)] == '.'
	}

	// coordinates are a claim's identity short of its clause: the
	// post-rewrite group a collision is judged within.
	type coordinates struct {
		requirement, backend, symbol string
		role                         stipulatorv1.BindingRole
	}
	var rows []RetargetRow
	var out []Update
	members := map[coordinates][]*stipulatorv1.Binding{}
	var spec *stipulatorv1.Spec
	var byID map[string]*stipulatorv1.Requirement
	// requirementFor is the corpus rule: the compiled requirement is
	// read only where a pair's identity needs it — a clause named by
	// label beside one named by ordinal — so a store without such a
	// pair keeps retarget corpus-free, a repair verb, and a corpus that
	// does not compile refuses the operation where a pair needs it.
	requirementFor := func(id string, a, b *stipulatorv1.Binding) (*stipulatorv1.Requirement, error) {
		if !(a.HasClauseLabel() && b.HasClauseOrdinal()) && !(a.HasClauseOrdinal() && b.HasClauseLabel()) {
			return nil, nil
		}
		if byID == nil {
			compiled, err := compileClean(fsys)
			if err != nil {
				return nil, fmt.Errorf("resolving the clause claims of %s for the collision check: %w", id, err)
			}
			spec = compiled
			byID = records.ByID(spec)
		}
		return byID[id], nil
	}
	type rewrite struct {
		b   *stipulatorv1.Binding
		new string
	}
	var rewrites []rewrite
	// A collision is two claims of one identity in the post-rewrite
	// store — requirement, backend, symbol, role, and the resolved
	// clause (REQ-evidence-clause-claim): two claims on distinct clauses
	// of one symbol are two claims, and the whole store is judged, the
	// untouched claims included, exactly as verification's hygiene
	// judges it.
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			target := b.GetBackend() == backend && matches(b.GetSymbol())
			sym := b.GetSymbol()
			if target {
				sym = newPrefix + b.GetSymbol()[len(oldPrefix):]
				rewrites = append(rewrites, rewrite{b: b, new: sym})
			}
			at := coordinates{requirement: b.GetRequirementId(), backend: b.GetBackend(), symbol: sym, role: b.GetRole()}
			for _, prior := range members[at] {
				req, err := requirementFor(at.requirement, prior, b)
				if err != nil {
					return nil, nil, nil, err
				}
				if records.ClaimIdentityAt(req, prior, sym) == records.ClaimIdentityAt(req, b, sym) {
					return nil, nil, nil, fmt.Errorf("retarget collides: the post-rewrite store would carry %s %s %s %s%s twice", at.requirement, at.backend, sym, at.role, records.ClauseSuffix(req, b))
				}
			}
			members[at] = append(members[at], b)
		}
	}
	if len(rewrites) == 0 {
		return nil, nil, nil, fmt.Errorf("no %s binding symbol matches prefix %q", backend, oldPrefix)
	}
	shapes := make(map[string]string, len(rewrites))
	for _, rw := range rewrites {
		if _, ok := shapes[rw.new]; ok {
			continue
		}
		res, shape, err := be.Resolve(rw.new)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("resolving replacement %s: %w", rw.new, err)
		}
		switch res {
		case verify.NotFound:
			return nil, nil, nil, fmt.Errorf("replacement symbol %s not found; the whole retarget is refused", rw.new)
		case verify.GeneratedFile:
			return nil, nil, nil, fmt.Errorf("replacement symbol %s is declared in a generated file; the whole retarget is refused", rw.new)
		}
		shapes[rw.new] = shape
	}
	touched := map[string]bool{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetBackend() != backend || !matches(b.GetSymbol()) {
				continue
			}
			old := b.GetSymbol()
			newSym := newPrefix + old[len(oldPrefix):]
			rows = append(rows, RetargetRow{Requirement: b.GetRequirementId(), Old: old, New: newSym, Role: b.GetRole()})
			b.SetSymbol(newSym)
			// Re-derive means exactly that: a pinned shape follows the
			// resolved replacement, an unpinned binding stays unpinned —
			// backfilling a pin is the pin verb's consent action, never
			// a retarget side effect (REQ-change-remediation).
			if b.GetShapeHash() != "" {
				b.SetShapeHash(shapes[newSym])
			}
			touched[bf.Path] = true
		}
	}
	for _, bf := range store.Bindings {
		if !touched[bf.Path] {
			continue
		}
		content, err := records.Render(bf)
		if err != nil {
			return nil, nil, nil, err
		}
		out = append(out, Update{Path: bf.Path, Content: content})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Requirement != rows[j].Requirement {
			return rows[i].Requirement < rows[j].Requirement
		}
		return rows[i].Old < rows[j].Old
	})
	sortUpdates(out)
	StampPriors(store, out)
	return out, rows, spec, nil
}
