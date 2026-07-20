// Package author is the write path for record claims: tool operations that
// validate at write time, so a claim is never born dangling, unresolvable,
// or stale. Humans and agents submit claims through these operations —
// never by hand-editing record files.
package author

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
	"google.golang.org/protobuf/proto"
)

// KnownBackends closes the backend-name set: a typo must never author an
// unvalidated binding, on any surface.
var KnownBackends = map[string]bool{"go": true, "proto": true}

// Roles maps CLI role names to the enum.
var Roles = map[string]stipulatorv1.BindingRole{
	"implements": stipulatorv1.BindingRole_BINDING_ROLE_IMPLEMENTS,
	"tests":      stipulatorv1.BindingRole_BINDING_ROLE_TESTS,
	"proves":     stipulatorv1.BindingRole_BINDING_ROLE_PROVES,
}

// ParseRole maps a role flag to the enum. Empty means unspecified (a
// wildcard for unbind, rejected by Bind); an unknown name is an error, so a
// typo can never silently widen an operation.
func ParseRole(s string) (stipulatorv1.BindingRole, error) {
	if s == "" {
		return stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED, nil
	}
	r, ok := Roles[s]
	if !ok {
		return 0, fmt.Errorf("unknown role %q (implements, tests, or proves)", s)
	}
	return r, nil
}

// NewLandingCondition builds a landing condition from mutually exclusive
// flag values; more than one set is an error. Fired marks a manual
// condition already discharged at declaration time — it is meaningless
// on the machine-evaluable conditions.
func NewLandingCondition(covered, exists, manual string, fired bool) (*stipulatorv1.LandingCondition, error) {
	set := 0
	for _, v := range []string{covered, exists, manual} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("conflicting landing conditions: give exactly one of covered, exists, manual")
	}
	if fired && manual == "" {
		return nil, fmt.Errorf("fired accompanies a manual condition (fire an existing gap with the fired flag alone)")
	}
	lc := &stipulatorv1.LandingCondition{}
	switch {
	case covered != "":
		lc.SetCovered(covered)
	case exists != "":
		lc.SetExists(exists)
	case manual != "":
		a := &stipulatorv1.ManualCondition{}
		a.SetCondition(manual)
		a.SetFired(fired)
		lc.SetManual(a)
	default:
		return nil, nil
	}
	return lc, nil
}

// BindRequest describes a binding to author.
type BindRequest struct {
	Requirement string
	Symbol      string
	Backend     string
	Role        stipulatorv1.BindingRole
	// File overrides the target binding file; empty derives
	// .stipulator/bindings/<second-id-segment>.textproto.
	File string
}

// Update is a file write the caller must apply.
type Update struct {
	Path    string
	Content []byte // nil means delete the file
}

// Bind validates and authors a binding: the requirement must exist in the
// compiled corpus; when the backend has a verifier, the symbol must resolve
// (a generated-file symbol is rejected) and the shape pin is captured; the
// content pin is always captured. A binding identical to an existing one is
// refused.
func Bind(fsys fs.FS, backends map[string]verify.Backend, req BindRequest) (*Update, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	if errs := compile.Errors(diags); len(errs) > 0 {
		return nil, fmt.Errorf("corpus does not compile: %s%s", errs[0], moreSuffix(len(errs)-1))
	}
	var contentHash string
	for _, r := range spec.GetRequirements() {
		if r.GetId() == req.Requirement {
			contentHash = r.GetContentHash()
		}
	}
	if contentHash == "" {
		return nil, fmt.Errorf("requirement %s is not in the corpus", req.Requirement)
	}
	if req.Role == stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
		return nil, fmt.Errorf("a role is required (implements, tests, or proves)")
	}
	if req.Symbol == "" || req.Backend == "" {
		return nil, fmt.Errorf("a backend and symbol are required")
	}
	if !KnownBackends[req.Backend] {
		return nil, fmt.Errorf("unknown backend %q (go, proto)", req.Backend)
	}
	if req.File != "" {
		clean := path.Clean(req.File)
		if clean != req.File || !strings.HasPrefix(clean, records.BindingsDir+"/") ||
			!strings.HasSuffix(clean, ".textproto") || strings.Contains(clean, "..") {
			return nil, fmt.Errorf("binding file must be a clean .textproto path under %s", records.BindingsDir)
		}
	}

	shapeHash := ""
	be, loaded := backends[req.Backend]
	if req.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES && !loaded {
		return nil, fmt.Errorf("no %s verifier is loaded to discharge %s as a proof; a proof claim that cannot be checked at write time is refused, not recorded", req.Backend, req.Symbol)
	}
	if loaded {
		res, shape, err := be.Resolve(req.Symbol)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", req.Symbol, err)
		}
		switch res {
		case verify.NotFound:
			return nil, fmt.Errorf("symbol %s not found", req.Symbol)
		case verify.GeneratedFile:
			return nil, fmt.Errorf("symbol %s is declared in a generated file; bind the generating artifact instead", req.Symbol)
		}
		shapeHash = shape
		if req.Role == stipulatorv1.BindingRole_BINDING_ROLE_PROVES {
			wc, ok := be.(verify.WitnessClassifier)
			if !ok || wc.WitnessClass(req.Symbol) != verify.AnalyzerProof {
				return nil, fmt.Errorf("the %s backend cannot discharge %s as a proof: bind an analyzer test (one invoking stipulate/structural), or use role tests", req.Backend, req.Symbol)
			}
		}
	}

	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetRequirementId() == req.Requirement && b.GetSymbol() == req.Symbol &&
				b.GetBackend() == req.Backend && b.GetRole() == req.Role {
				return nil, fmt.Errorf("identical binding already exists in %s", bf.Path)
			}
		}
	}

	b := &stipulatorv1.Binding{}
	b.SetRequirementId(req.Requirement)
	b.SetContentHash(contentHash)
	b.SetBackend(req.Backend)
	b.SetSymbol(req.Symbol)
	b.SetRole(req.Role)
	if shapeHash != "" {
		b.SetShapeHash(shapeHash)
	}

	file := req.File
	if file == "" {
		file = defaultBindingFile(req.Requirement)
	}
	content, err := records.AddBinding(store, file, b)
	if err != nil {
		return nil, err
	}
	return &Update{Path: file, Content: content}, nil
}

// Unbind removes bindings matching the request (symbol and role narrowing
// optional) and returns the file writes; matching nothing is an error.
func Unbind(fsys fs.FS, requirement, symbol string, role stipulatorv1.BindingRole) ([]Update, int, error) {
	store, err := records.Load(fsys)
	if err != nil {
		return nil, 0, err
	}
	updates, deletions, removed, err := records.RemoveBindings(store, func(b *stipulatorv1.Binding) bool {
		if b.GetRequirementId() != requirement {
			return false
		}
		if symbol != "" && b.GetSymbol() != symbol {
			return false
		}
		if role != stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED && b.GetRole() != role {
			return false
		}
		return true
	})
	if err != nil {
		return nil, 0, err
	}
	if removed == 0 {
		return nil, 0, fmt.Errorf("no binding matches %s", requirement)
	}
	var out []Update
	for p, c := range updates {
		out = append(out, Update{Path: p, Content: c})
	}
	for _, p := range deletions {
		out = append(out, Update{Path: p, Content: nil})
	}
	sortUpdates(out)
	return out, removed, nil
}

// Gap validates and authors a gap record: the requirement must exist and
// a reason and a landing condition are required. Declaring over an
// existing gap updates it in place — a gap's reason evolves with the
// code — and the prior record is returned so a changed landing condition
// is surfaced, never silently retargeted.
func Gap(fsys fs.FS, g *stipulatorv1.Gap) (*Update, *stipulatorv1.Gap, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, nil, err
	}
	if errs := compile.Errors(diags); len(errs) > 0 {
		return nil, nil, fmt.Errorf("corpus does not compile: %s%s", errs[0], moreSuffix(len(errs)-1))
	}
	found := false
	for _, r := range spec.GetRequirements() {
		if r.GetId() == g.GetRequirementId() {
			found = true
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("requirement %s is not in the corpus", g.GetRequirementId())
	}
	if g.GetReason() == "" {
		return nil, nil, fmt.Errorf("a reason is required")
	}
	if !g.HasLands() {
		return nil, nil, fmt.Errorf("a landing condition is required")
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}
	target := records.GapPath(g.GetRequirementId())
	var prior *stipulatorv1.Gap
	for _, gf := range store.Gaps {
		if gf.Gap.GetRequirementId() == g.GetRequirementId() {
			// Update in place, at the record's existing path.
			target = gf.Path
			prior = gf.Gap
		}
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
				return nil, nil, fmt.Errorf("%s holds a gap for %s; refusing to overwrite", target, gf.Gap.GetRequirementId())
			}
		}
	}
	return &Update{Path: target, Content: records.RenderGap(g)}, prior, nil
}

// LandingConditionString renders a landing condition human-readably, for
// surfacing retargets.
func LandingConditionString(lc *stipulatorv1.LandingCondition) string {
	switch {
	case lc == nil:
		return "none"
	case lc.HasCovered():
		return "covered(" + lc.GetCovered() + ")"
	case lc.HasExists():
		return "exists(" + lc.GetExists() + ")"
	case lc.HasManual():
		s := "manual(" + lc.GetManual().GetCondition() + ")"
		if lc.GetManual().GetFired() {
			s += " [fired]"
		}
		return s
	}
	return "none"
}

// defaultBindingFile groups bindings by the identifier's second segment:
// REQ-profile-… lands in .stipulator/bindings/profile.textproto.
func defaultBindingFile(requirement string) string {
	segs := strings.Split(requirement, "-")
	name := "bindings"
	if len(segs) >= 2 {
		name = segs[1]
	}
	return path.Join(records.BindingsDir, name+".textproto")
}

// moreSuffix renders "(and N more)" only when there are more.
func moreSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(" (and %d more)", n)
}

func sortUpdates(u []Update) {
	sort.Slice(u, func(i, j int) bool { return u[i].Path < u[j].Path })
}

// Init scaffolds the manifest for a fresh corpus with the default include,
// refusing when one already exists.
func Init(fsys fs.FS) (*Update, error) {
	if _, err := fs.Stat(fsys, corpus.ManifestPath); err == nil {
		return nil, fmt.Errorf("%s already exists; this is already a stipulator repository", corpus.ManifestPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	content := "# proto-file: proto/stipulator/v1/manifest.proto\n" +
		"# proto-message: stipulator.v1.Manifest\n\n" +
		"include: " + strconv.Quote(corpus.DefaultInclude) + "\n"
	return &Update{Path: corpus.ManifestPath, Content: []byte(content)}, nil
}

// SelfSentinel is the bulk landing sentinel: covered(self) resolves to
// each named requirement's own coverage (REQ-gap-bulk). It can never
// collide with a real identifier — the profile's ID pattern requires the
// REQ- prefix.
const SelfSentinel = "self"

// Gaps declares one gap per requirement, all sharing a reason and landing
// condition — the spec-ahead-of-code bulk case. A covered(self) condition
// resolves to each requirement's own coverage. Each record is an ordinary
// per-requirement gap and lands independently; validation is all-or-nothing
// so a typo mid-list declares nothing. Updated gaps whose landing
// condition changed are surfaced in the returned notes — a retarget is
// never silent.
func Gaps(fsys fs.FS, reqs []string, reason string, lands *stipulatorv1.LandingCondition) ([]Update, []string, error) {
	if len(reqs) == 0 {
		return nil, nil, fmt.Errorf("at least one requirement is required")
	}
	var out []Update
	var notes []string
	seenPath := map[string]bool{}
	for _, id := range reqs {
		g := &stipulatorv1.Gap{}
		g.SetRequirementId(id)
		g.SetReason(reason)
		wantUnfired := false
		if lands != nil {
			each := proto.CloneOf(lands)
			if each.HasCovered() && each.GetCovered() == SelfSentinel {
				each.SetCovered(id)
			}
			wantUnfired = each.HasManual() && !each.GetManual().GetFired()
			g.SetLands(each)
		}
		up, prior, err := Gap(fsys, g)
		if err != nil {
			return nil, nil, err
		}
		if seenPath[up.Path] {
			return nil, nil, fmt.Errorf("requirement %s repeats in the list", id)
		}
		seenPath[up.Path] = true
		switch {
		case prior != nil && !proto.Equal(prior.GetLands(), g.GetLands()):
			notes = append(notes, id+": landing retargeted "+
				LandingConditionString(prior.GetLands())+" -> "+LandingConditionString(g.GetLands()))
		// Preservation overriding an explicitly unfired declaration is
		// surfaced like any other non-silent consequence (REQ-gap-verb):
		// after preservation the old and new conditions compare equal, so
		// the retarget note above cannot fire for it.
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
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	if errs := compile.Errors(diags); len(errs) > 0 {
		return nil, fmt.Errorf("corpus does not compile: %s%s", errs[0], moreSuffix(len(errs)-1))
	}
	present := map[string]bool{}
	for _, r := range spec.GetRequirements() {
		present[r.GetId()] = true
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
		if !present[id] {
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
					id, LandingConditionString(gf.Gap.GetLands()))
			}
			g := proto.CloneOf(gf.Gap)
			g.GetLands().GetManual().SetFired(true)
			out = append(out, Update{Path: gf.Path, Content: records.RenderGap(g)})
		}
		if !found {
			return nil, fmt.Errorf("no gap record names %s; declare it before firing", id)
		}
	}
	sortUpdates(out)
	return out, nil
}

// PruneDanglingGaps returns deletions for every gap record naming a
// requirement absent from the corpus — the explicit bulk repair,
// judged against the compiled corpus alone (REQ-gap-prune-dangling).
func PruneDanglingGaps(store *records.Store, present map[string]bool) []Update {
	var out []Update
	for _, gf := range store.Gaps {
		if !present[gf.Gap.GetRequirementId()] {
			out = append(out, Update{Path: gf.Path, Content: nil})
		}
	}
	sortUpdates(out)
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
	return out
}
