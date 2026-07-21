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
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"

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
// NewExcuses parses declared excuse classes — uncovered, stale, broken
// — validating each name (REQ-gap-verb). Empty input declares nothing:
// the record's default, uncovered alone, applies (REQ-gap-record).
func NewExcuses(names []string) ([]stipulatorv1.GapExcuse, error) {
	var out []stipulatorv1.GapExcuse
	for _, n := range names {
		switch n {
		case "uncovered":
			out = append(out, stipulatorv1.GapExcuse_GAP_EXCUSE_UNCOVERED)
		case "stale":
			out = append(out, stipulatorv1.GapExcuse_GAP_EXCUSE_STALE)
		case "broken":
			out = append(out, stipulatorv1.GapExcuse_GAP_EXCUSE_BROKEN)
		default:
			return nil, fmt.Errorf("unknown excuse class %q (uncovered, stale, broken)", n)
		}
	}
	return out, nil
}

// ExcuseString renders one excuse class for messages.
func ExcuseString(x stipulatorv1.GapExcuse) string {
	switch x {
	case stipulatorv1.GapExcuse_GAP_EXCUSE_UNCOVERED:
		return "uncovered"
	case stipulatorv1.GapExcuse_GAP_EXCUSE_STALE:
		return "stale"
	case stipulatorv1.GapExcuse_GAP_EXCUSE_BROKEN:
		return "broken"
	}
	return x.String()
}

// ExcusesString renders a declared excuse set, naming the default when
// nothing is declared.
func ExcusesString(xs []stipulatorv1.GapExcuse) string {
	if len(xs) == 0 {
		return "uncovered (default)"
	}
	names := make([]string, 0, len(xs))
	for _, x := range xs {
		names = append(names, ExcuseString(x))
	}
	return strings.Join(names, ", ")
}

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
		// Set only when true: an explicit false would give the field
		// presence, making proto.Equal see a retarget against every prior
		// record that simply lacks it.
		if fired {
			a.SetFired(true)
		}
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
	// Prior is the raw content the computing operation read at Path —
	// the compare-and-swap precondition (REQ-record-cas): the applier
	// refuses when the file moved in between. PriorAbsent means the
	// operation saw no file there.
	Prior       []byte
	PriorAbsent bool
}

// StampPriors records, on each update, the content the operation read
// for its target file from its loaded store — the compare-and-swap
// precondition (REQ-record-cas). A path outside the store stamps as
// read-absent.
func StampPriors(store *records.Store, ups []Update) {
	raw := map[string][]byte{}
	for _, bf := range store.Bindings {
		raw[bf.Path] = bf.Raw
	}
	for _, gf := range store.Gaps {
		raw[gf.Path] = gf.Raw
	}
	for _, af := range store.Attestations {
		raw[af.Path] = af.Raw
	}
	if store.TombstonesRaw != nil {
		raw[records.TombstonesPath] = store.TombstonesRaw
	}
	for i := range ups {
		if b, ok := raw[ups[i].Path]; ok {
			ups[i].Prior = b
		} else {
			ups[i].PriorAbsent = true
		}
	}
}

func stampPrior(store *records.Store, up *Update) {
	tmp := []Update{*up}
	StampPriors(store, tmp)
	*up = tmp[0]
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
	up := &Update{Path: file, Content: content}
	stampPrior(store, up)
	return up, nil
}

// Binds authors many binding claims in one call, validating
// all-or-nothing: each claim validates against the tree with every
// earlier claim's pending write applied — same-file claims merge — and
// a failure anywhere authors nothing (REQ-mcp-tools).
func Binds(fsys fs.FS, backends map[string]verify.Backend, reqs []BindRequest) ([]Update, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("at least one claim is required")
	}
	base, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	over := batchFS{base: fsys, mem: fstest.MapFS{}}
	latest := map[string]Update{}
	for i, r := range reqs {
		up, err := Bind(over, backends, r)
		if err != nil {
			return nil, fmt.Errorf("claim %d (%s %s): %w", i+1, r.Requirement, r.Symbol, err)
		}
		over.mem[up.Path] = &fstest.MapFile{Data: up.Content}
		latest[up.Path] = *up
	}
	out := make([]Update, 0, len(latest))
	for _, up := range latest {
		out = append(out, up)
	}
	sortUpdates(out)
	// Priors come from the BASE store: a Bind inside the batch stamped
	// against the overlay, whose pending content is this batch's own,
	// never what sits on disk (REQ-record-cas).
	for i := range out {
		out[i].Prior, out[i].PriorAbsent = nil, false
	}
	StampPriors(base, out)
	return out, nil
}

// batchFS lays a batch's pending record writes over the base tree, so
// a later claim validates against the earlier claims' effects without
// touching disk until the whole batch validates.
type batchFS struct {
	base fs.FS
	mem  fstest.MapFS
}

func (o batchFS) Open(name string) (fs.File, error) {
	if _, ok := o.mem[name]; ok {
		return o.mem.Open(name)
	}
	return o.base.Open(name)
}

// ReadDir merges the overlay's entries into the base directory listing —
// a batch-created record file must be visible to the next claim's store
// load — with the overlay winning on name collisions.
func (o batchFS) ReadDir(name string) ([]fs.DirEntry, error) {
	baseEntries, baseErr := fs.ReadDir(o.base, name)
	if baseErr != nil && !errors.Is(baseErr, fs.ErrNotExist) {
		return nil, baseErr
	}
	memEntries, memErr := fs.ReadDir(o.mem, name)
	if memErr != nil && !errors.Is(memErr, fs.ErrNotExist) {
		return nil, memErr
	}
	if baseErr != nil && memErr != nil {
		return nil, baseErr
	}
	merged := map[string]fs.DirEntry{}
	for _, e := range baseEntries {
		merged[e.Name()] = e
	}
	for _, e := range memEntries {
		merged[e.Name()] = e
	}
	names := make([]string, 0, len(merged))
	for n := range merged {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]fs.DirEntry, 0, len(names))
	for _, n := range names {
		out = append(out, merged[n])
	}
	return out, nil
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
	StampPriors(store, out)
	return out, removed, nil
}

// RetargetRow reports one rewritten binding identity.
type RetargetRow struct {
	Requirement string
	Old, New    string
	Role        stipulatorv1.BindingRole
}

// RetargetSymbols rewrites stored binding symbols for one backend under
// an exact old-prefix-to-new-prefix mapping — the module-rename repair
// (REQ-change-retarget). A symbol matches only at a path or member
// boundary, so a prefix never captures a sibling that merely shares
// characters. All-or-nothing: every replacement must resolve through
// the backend (shape pins re-derive from those resolutions; content
// pins ride unchanged — the requirement text did not move), and a
// rewrite colliding with any post-rewrite binding of the same
// requirement, backend, symbol, and role refuses the whole batch. The
// returned rows report every old-to-new identity; callers preview by
// discarding the updates.
func RetargetSymbols(fsys fs.FS, backends map[string]verify.Backend, backend, oldPrefix, newPrefix string) ([]Update, []RetargetRow, error) {
	if backend == "" || oldPrefix == "" || newPrefix == "" {
		return nil, nil, fmt.Errorf("a backend, an old prefix, and a new prefix are required")
	}
	if !KnownBackends[backend] {
		return nil, nil, fmt.Errorf("unknown backend %q (go, proto)", backend)
	}
	if oldPrefix == newPrefix {
		return nil, nil, fmt.Errorf("old and new prefixes are identical; nothing to retarget")
	}
	be, loaded := backends[backend]
	if !loaded {
		return nil, nil, fmt.Errorf("no %s backend is loaded to resolve replacements; a retarget that cannot validate its rewrites is refused, not recorded", backend)
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, nil, err
	}

	// A prefix matches at a boundary only: the next rune after it is a
	// path separator or the member dot, never a bare character run.
	matches := func(symbol string) bool {
		if !strings.HasPrefix(symbol, oldPrefix) || len(symbol) == len(oldPrefix) {
			return false
		}
		return symbol[len(oldPrefix)] == '/' || symbol[len(oldPrefix)] == '.'
	}

	type identity struct {
		requirement, backend, symbol string
		role                         stipulatorv1.BindingRole
	}
	var rows []RetargetRow
	var out []Update
	seen := map[identity]bool{}
	type rewrite struct {
		b   *stipulatorv1.Binding
		new string
	}
	var rewrites []rewrite
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			target := b.GetBackend() == backend && matches(b.GetSymbol())
			sym := b.GetSymbol()
			if target {
				sym = newPrefix + b.GetSymbol()[len(oldPrefix):]
				rewrites = append(rewrites, rewrite{b: b, new: sym})
			}
			id := identity{requirement: b.GetRequirementId(), backend: b.GetBackend(), symbol: sym, role: b.GetRole()}
			if seen[id] {
				return nil, nil, fmt.Errorf("retarget collides: the post-rewrite store would carry %s %s %s %s twice", id.requirement, id.backend, sym, id.role)
			}
			seen[id] = true
		}
	}
	if len(rewrites) == 0 {
		return nil, nil, fmt.Errorf("no %s binding symbol matches prefix %q", backend, oldPrefix)
	}
	shapes := make(map[string]string, len(rewrites))
	for _, rw := range rewrites {
		if _, ok := shapes[rw.new]; ok {
			continue
		}
		res, shape, err := be.Resolve(rw.new)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving replacement %s: %w", rw.new, err)
		}
		switch res {
		case verify.NotFound:
			return nil, nil, fmt.Errorf("replacement symbol %s not found; the whole retarget is refused", rw.new)
		case verify.GeneratedFile:
			return nil, nil, fmt.Errorf("replacement symbol %s is declared in a generated file; the whole retarget is refused", rw.new)
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
			return nil, nil, err
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
	return out, rows, nil
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
	seenExcuse := map[stipulatorv1.GapExcuse]bool{}
	for _, x := range g.GetExcuses() {
		if x != stipulatorv1.GapExcuse_GAP_EXCUSE_UNCOVERED &&
			x != stipulatorv1.GapExcuse_GAP_EXCUSE_STALE &&
			x != stipulatorv1.GapExcuse_GAP_EXCUSE_BROKEN {
			return nil, nil, fmt.Errorf("excuse classes are uncovered, stale, or broken")
		}
		if seenExcuse[x] {
			return nil, nil, fmt.Errorf("excuse class %s repeats", ExcuseString(x))
		}
		seenExcuse[x] = true
	}
	// Canonical order by enum value: declaration order carries no
	// meaning, so equal sets compare equal and never read as a rescope.
	slices.Sort(g.GetExcuses())
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
	up := &Update{Path: target, Content: records.RenderGap(g)}
	stampPrior(store, up)
	return up, prior, nil
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
	return &Update{Path: corpus.ManifestPath, Content: []byte(content), PriorAbsent: true}, nil
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
func Gaps(fsys fs.FS, reqs []string, reason string, lands *stipulatorv1.LandingCondition, excuses []stipulatorv1.GapExcuse) ([]Update, []string, error) {
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
		g.SetExcuses(excuses)
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
		// A changed excuse set is surfaced exactly as a changed landing
		// condition is (REQ-gap-verb): rescoping which reds a standing
		// record absorbs is never silent.
		case prior != nil && !slices.Equal(prior.GetExcuses(), g.GetExcuses()):
			notes = append(notes, id+": excuses rescoped "+
				ExcusesString(prior.GetExcuses())+" -> "+ExcusesString(g.GetExcuses()))
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
	StampPriors(store, out)
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
