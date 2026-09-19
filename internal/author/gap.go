package author

import (
	"fmt"
	"io/fs"
	"slices"
	"sort"

	"google.golang.org/protobuf/proto"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/profile"
	"github.com/greatliontech/stipulator/internal/records"
)

// The gap lifecycle: declaring, retracting, firing, and pruning gap
// records.

// Gap validates and authors one gap record: the requirement must exist
// and a reason and a landing condition are required. Declaring over an
// existing gap updates it in place — a gap's reason evolves with the
// code — and the prior record is returned so a changed landing condition
// is surfaced, never silently retargeted. Every surface declares through
// Gaps; this single form is the bulk form of one, kept for the unit
// pins that read the prior record and the notes per declaration.
func Gap(fsys fs.FS, g *stipulatorv1.Gap) (*Update, *stipulatorv1.Gap, []string, error) {
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, nil, err
	}
	return gapOver(spec, store, g)
}

// gapOver declares one gap against an already compiled corpus and
// loaded store — the one declaration path, so the bulk form compiles
// and loads once for every requirement it names and the single form
// is the bulk form of one. A covered(self) landing condition resolves
// here to the requirement's own coverage (REQ-gap-bulk): the sentinel
// and its validation live on one side of the entry point.
func gapOver(spec *stipulatorv1.Spec, store *records.Store, g *stipulatorv1.Gap) (*Update, *stipulatorv1.Gap, []string, error) {
	hashes := records.HashesOf(spec)
	if !hashes.Known(g.GetRequirementId()) {
		return nil, nil, nil, fmt.Errorf("requirement %s is not in the corpus", g.GetRequirementId())
	}
	if g.HasLands() && g.GetLands().HasCovered() && g.GetLands().GetCovered() == SelfSentinel {
		g.GetLands().SetCovered(g.GetRequirementId())
	}
	if g.GetReason() == "" {
		return nil, nil, nil, fmt.Errorf("a reason is required")
	}
	if !g.HasLands() {
		return nil, nil, nil, fmt.Errorf("a landing condition is required")
	}
	// A machine-evaluable landing target must be able to name a
	// requirement, or the gap is born unfireable: free-text prose in
	// covered/exists would dangle forever with the gap permanently open
	// and no triage surface marking it due — prose belongs in a manual
	// condition (REQ-gap-conditions). A well-formed identifier absent
	// from the corpus is NOT refused: naming a requirement before it is
	// authored is the prospective use these conditions exist for
	// (REQ-gap-verb) — it is surfaced in the notes instead, so a typo'd
	// id is loud at declaration without foreclosing the future one.
	var notes []string
	for _, t := range []struct {
		form   string
		has    bool
		target string
	}{
		{"covered", g.GetLands().HasCovered(), g.GetLands().GetCovered()},
		{"exists", g.GetLands().HasExists(), g.GetLands().GetExists()},
	} {
		switch {
		case !t.has:
		case !profile.ValidID(t.target):
			return nil, nil, nil, fmt.Errorf("%s(%s) does not match the requirement identifier grammar; a prose condition belongs in manual", t.form, t.target)
		case !hashes.Known(t.target):
			notes = append(notes, fmt.Sprintf("%s: %s(%s) names no current requirement — the condition waits for it to exist; retract and redeclare if this is a typo", g.GetRequirementId(), t.form, t.target))
		}
	}
	// The declaration consents to the requirement's CURRENT text: the
	// content pin is the consent surface, exactly as a binding's
	// (REQ-gap-consent). A re-declaration is a fresh consent.
	g.SetContentHash(hashes.Content[g.GetRequirementId()])
	g.SetSourceHash(hashes.Source[g.GetRequirementId()])
	seenExcuse := map[stipulatorv1.GapExcuse]bool{}
	for _, x := range g.GetExcuses() {
		if x != stipulatorv1.GapExcuse_GAP_EXCUSE_UNCOVERED &&
			x != stipulatorv1.GapExcuse_GAP_EXCUSE_STALE &&
			x != stipulatorv1.GapExcuse_GAP_EXCUSE_BROKEN {
			return nil, nil, nil, fmt.Errorf("excuse classes are uncovered, stale, or broken")
		}
		if seenExcuse[x] {
			return nil, nil, nil, fmt.Errorf("excuse class %s repeats", excuseString(x))
		}
		seenExcuse[x] = true
	}
	// Canonical order by enum value: declaration order carries no
	// meaning, so equal sets compare equal and never read as a rescope.
	slices.Sort(g.GetExcuses())
	target := records.GapPath(g.GetRequirementId())
	var prior *stipulatorv1.Gap
	var priorRaw []byte
	for _, gf := range store.Gaps {
		if gf.Gap.GetRequirementId() == g.GetRequirementId() {
			// Update in place, at the record's existing path.
			target = gf.Path
			prior = gf.Gap
			priorRaw = gf.Raw
		}
	}
	// A re-stamp over a differing consent is surfaced, never silent: the
	// re-declaration is a fresh consent (REQ-gap-verb), but discharging
	// the exact drift REQ-gap-consent exists to expose deserves a note
	// even when the declarer only meant to touch the reason.
	if prior != nil && prior.GetContentHash() != "" && prior.GetContentHash() != g.GetContentHash() {
		notes = append(notes, fmt.Sprintf("%s: consent re-stamped — the requirement's text changed since the prior declaration", g.GetRequirementId()))
	}
	// An unchanged manual condition keeps its fired state: an unfire is a
	// lifecycle retarget, so it only happens through an explicit changed
	// declaration, never as a side effect of re-declaring (REQ-gap-verb).
	if prior != nil && g.GetLands().HasManual() && prior.GetLands().HasManual() &&
		g.GetLands().GetManual().GetCondition() == prior.GetLands().GetManual().GetCondition() &&
		prior.GetLands().GetManual().GetFired() {
		g.GetLands().GetManual().SetFired(true)
	}
	if prior == nil {
		// Gap file layout is free, so another requirement's record may
		// legally sit at this requirement's canonical path — never
		// overwrite it.
		for _, gf := range store.Gaps {
			if gf.Path == target {
				return nil, nil, nil, fmt.Errorf("%s holds a gap for %s; refusing to overwrite", target, gf.Gap.GetRequirementId())
			}
		}
	}
	// Every write goes through the machine-owned gap writer — its comment
	// refusal and header preservation (REQ-evidence-binding-machine-owned);
	// a fresh record (nil Raw) renders with the standard header.
	content, err := records.RenderGapFile(records.GapFile{Path: target, Raw: priorRaw, Gap: g})
	if err != nil {
		return nil, nil, nil, err
	}
	up := &Update{Path: target, Content: content}
	stampPrior(store, up)
	return up, prior, notes, nil
}

// Gaps declares one gap per requirement, all sharing a reason and landing
// condition — the spec-ahead-of-code bulk case. A covered(self) condition
// resolves to each requirement's own coverage. Each record is an ordinary
// per-requirement gap and lands independently; validation is all-or-nothing
// so a typo mid-list declares nothing. Updated gaps whose landing
// condition changed are surfaced in the returned notes — a retarget is
// never silent.
func Gaps(fsys fs.FS, reqs []string, reason string, lands *stipulatorv1.LandingCondition, excuses []stipulatorv1.GapExcuse) ([]Update, []string, error) {
	if len(reqs) == 0 {
		return nil, nil, fmt.Errorf("at least one requirement is required")
	}
	// One compile and one load for the whole list: each record is an
	// ordinary declaration over the same corpus.
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, nil, err
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	var out []Update
	var notes []string
	seenPath := map[string]bool{}
	for _, id := range reqs {
		g := &stipulatorv1.Gap{}
		g.SetRequirementId(id)
		g.SetReason(reason)
		g.SetExcuses(excuses)
		wantUnfired := false
		if lands != nil {
			each := proto.CloneOf(lands)
			wantUnfired = each.HasManual() && !each.GetManual().GetFired()
			g.SetLands(each)
		}
		up, prior, gapNotes, err := gapOver(spec, store, g)
		if err != nil {
			return nil, nil, err
		}
		notes = append(notes, gapNotes...)
		if seenPath[up.Path] {
			return nil, nil, fmt.Errorf("requirement %s repeats in the list", id)
		}
		seenPath[up.Path] = true
		switch {
		case prior != nil && !proto.Equal(prior.GetLands(), g.GetLands()):
			notes = append(notes, id+": landing retargeted "+
				landingConditionString(prior.GetLands())+" -> "+landingConditionString(g.GetLands()))
		// A changed excuse set is surfaced exactly as a changed landing
		// condition is (REQ-gap-verb): rescoping which reds a standing
		// record absorbs is never silent.
		case prior != nil && !slices.Equal(prior.GetExcuses(), g.GetExcuses()):
			notes = append(notes, id+": excuses rescoped "+
				excusesString(prior.GetExcuses())+" -> "+excusesString(g.GetExcuses()))
		// Preservation overriding an explicitly unfired declaration is
		// surfaced like any other non-silent consequence (REQ-gap-verb):
		// with the condition otherwise unchanged the old and new compare
		// equal after preservation, so the retarget note above cannot
		// fire for it; a class flip on the same text is a retarget, whose
		// note shows the preserved firing on its right-hand side.
		case wantUnfired && g.GetLands().GetManual().GetFired():
			notes = append(notes, id+": fired state preserved (unfire requires a changed condition, or retract and redeclare)")
		}
		out = append(out, *up)
	}
	sortUpdates(out)
	sort.Strings(notes)
	return out, notes, nil
}

// RetractGaps deletes the gap records naming the given requirements —
// dangling records included: the dangling state is what retraction
// repairs, so no corpus validation gates it, and the tombstone registry
// is never touched (retraction withdraws a declaration, never the
// requirement). A requirement with no gap record is an error, and the
// batch applies all-or-nothing (REQ-gap-retract).
func RetractGaps(fsys fs.FS, reqs []string) ([]Update, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("at least one requirement is required")
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	var out []Update
	seen := map[string]bool{}
	for _, id := range reqs {
		if seen[id] {
			return nil, fmt.Errorf("requirement %s repeats in the list", id)
		}
		seen[id] = true
		found := false
		for _, gf := range store.Gaps {
			if gf.Gap.GetRequirementId() == id {
				out = append(out, Update{Path: gf.Path, Content: nil})
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("no gap record names %s; nothing to retract", id)
		}
	}
	sortUpdates(out)
	StampPriors(store, out)
	return out, nil
}

// FireGaps marks existing gaps' manual landing conditions fired — the
// external judgment entering the record system through the same
// validated path as the declaration it discharges (REQ-gap-verb): the
// requirement is validated against the compiled corpus exactly as a
// declaration is, so firing a dangling record errors toward its real
// repair, retraction. A missing record or a non-manual condition is an
// error; the batch validates all-or-nothing, and firing an already-fired
// record is a no-op write, not an error.
func FireGaps(fsys fs.FS, reqs []string) ([]Update, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("at least one requirement is required")
	}
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, err
	}
	corpus := records.HashesOf(spec)
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	var out []Update
	seen := map[string]bool{}
	for _, id := range reqs {
		if seen[id] {
			return nil, fmt.Errorf("requirement %s repeats in the list", id)
		}
		seen[id] = true
		if !corpus.Known(id) {
			return nil, fmt.Errorf("%s is not in the corpus; a dangling gap's repair is retraction, not firing", id)
		}
		found := false
		for _, gf := range store.Gaps {
			if gf.Gap.GetRequirementId() != id {
				continue
			}
			found = true
			if !gf.Gap.GetLands().HasManual() {
				return nil, fmt.Errorf("%s's landing condition is %s, not manual; only a manual condition fires",
					id, landingConditionString(gf.Gap.GetLands()))
			}
			g := proto.CloneOf(gf.Gap)
			g.GetLands().GetManual().SetFired(true)
			content, err := records.RenderGapFile(records.GapFile{Path: gf.Path, Raw: gf.Raw, Gap: g})
			if err != nil {
				return nil, err
			}
			out = append(out, Update{Path: gf.Path, Content: content})
		}
		if !found {
			return nil, fmt.Errorf("no gap record names %s; declare it before firing", id)
		}
	}
	sortUpdates(out)
	StampPriors(store, out)
	return out, nil
}

// PruneDanglingGaps returns deletions for every gap record naming a
// requirement absent from the corpus — the explicit bulk repair,
// judged against the compiled corpus alone (REQ-gap-prune-dangling).
func PruneDanglingGaps(store *records.Store, corpus records.Hashes) []Update {
	var out []Update
	for _, gf := range store.Gaps {
		if !corpus.Known(gf.Gap.GetRequirementId()) {
			out = append(out, Update{Path: gf.Path, Content: nil})
		}
	}
	sortUpdates(out)
	StampPriors(store, out)
	return out
}

// PruneResolvedGaps returns deletions for every gap whose requirement ids
// are in resolved — the prune operation's record edits.
func PruneResolvedGaps(store *records.Store, resolved map[string]bool) []Update {
	var out []Update
	for _, gf := range store.Gaps {
		if resolved[gf.Gap.GetRequirementId()] {
			out = append(out, Update{Path: gf.Path, Content: nil})
		}
	}
	sortUpdates(out)
	StampPriors(store, out)
	return out
}
