// Package author is the write path for record claims: tool operations that
// validate at write time, so a claim is never born dangling, unresolvable,
// or stale. Humans and agents submit claims through these operations —
// never by hand-editing record files.
package author

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
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
// flag values; more than one set is an error.
func NewLandingCondition(covered, exists, attested string) (*stipulatorv1.LandingCondition, error) {
	set := 0
	for _, v := range []string{covered, exists, attested} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("conflicting landing conditions: give exactly one of covered, exists, attested")
	}
	lc := &stipulatorv1.LandingCondition{}
	switch {
	case covered != "":
		lc.SetCovered(covered)
	case exists != "":
		lc.SetExists(exists)
	case attested != "":
		a := &stipulatorv1.Attested{}
		a.SetCondition(attested)
		lc.SetAttested(a)
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
	if len(diags) > 0 {
		return nil, fmt.Errorf("corpus does not compile: %s%s", diags[0], moreSuffix(len(diags)-1))
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
	if be, ok := backends[req.Backend]; ok {
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

// Gap validates and authors a gap record: the requirement must exist, a
// reason and a landing condition are required, and an existing record for
// the requirement is refused.
func Gap(fsys fs.FS, g *stipulatorv1.Gap) (*Update, error) {
	spec, diags, err := compile.Compile(fsys)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, fmt.Errorf("corpus does not compile: %s%s", diags[0], moreSuffix(len(diags)-1))
	}
	found := false
	for _, r := range spec.GetRequirements() {
		if r.GetId() == g.GetRequirementId() {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("requirement %s is not in the corpus", g.GetRequirementId())
	}
	if g.GetReason() == "" {
		return nil, fmt.Errorf("a reason is required")
	}
	if !g.HasLands() {
		return nil, fmt.Errorf("a landing condition is required")
	}
	store, err := records.Load(fsys)
	if err != nil {
		return nil, err
	}
	target := records.GapPath(g.GetRequirementId())
	for _, gf := range store.Gaps {
		if gf.Gap.GetRequirementId() == g.GetRequirementId() {
			return nil, fmt.Errorf("a gap for %s already exists at %s", g.GetRequirementId(), gf.Path)
		}
		// Gap file layout is free, so another requirement's record may
		// legally sit at this requirement's canonical path — never
		// overwrite it.
		if gf.Path == target {
			return nil, fmt.Errorf("%s holds a gap for %s; refusing to overwrite", target, gf.Gap.GetRequirementId())
		}
	}
	return &Update{Path: target, Content: records.RenderGap(g)}, nil
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
