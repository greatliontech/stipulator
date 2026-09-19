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
	"testing/fstest"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"github.com/greatliontech/stipulator/internal/corpus"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/internal/verify"
)

// BindRequest describes a binding to author.
type BindRequest struct {
	Requirement string
	Symbol      string
	Backend     string
	Role        stipulatorv1.BindingRole
	// File overrides the target binding file; empty derives
	// .stipulator/bindings/<second-id-segment>.textproto.
	File string
	// Clause scopes the claim to one payload clause of the requirement:
	// an ordinal (all digits, from 1) or a label; empty claims the
	// whole requirement (REQ-evidence-clause-claim).
	Clause string
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
	// Document marks a corpus-document rewrite — the enforcement
	// pointers a retarget moved (REQ-change-enforcement-pointers), the
	// one update that lands outside the record stores; an applier admits
	// it only for a document the corpus names.
	Document bool
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

// bind validates and authors one binding claim — Binds' per-claim step: the requirement must exist in the
// compiled corpus; when the backend has a verifier, the symbol must resolve
// (a generated-file symbol is rejected) and the shape pin is captured; the
// content pin is always captured. A binding identical to an existing one is
// refused.
func bind(fsys fs.FS, backends map[string]verify.Backend, req BindRequest) (*Update, error) {
	spec, err := compileClean(fsys)
	if err != nil {
		return nil, err
	}
	target, ok := records.ByID(spec)[req.Requirement]
	if !ok {
		return nil, fmt.Errorf("requirement %s is not in the corpus", req.Requirement)
	}
	contentHash, sourceHash := target.GetContentHash(), target.GetSourceHash()
	claim := &stipulatorv1.Binding{}
	if err := records.SetClause(claim, req.Clause); err != nil {
		return nil, fmt.Errorf("claim on %s: %w", req.Requirement, err)
	}
	// A clause claim resolves against the compiled requirement at write
	// time, so a claim on a clause that does not exist is refused, never
	// recorded to read as a dangling record later.
	if _, ok := records.ResolveClause(target, claim); !ok {
		return nil, fmt.Errorf("%s declares no %s: %s", req.Requirement, records.ClauseName(claim), records.ClausesOffered(target))
	}
	if req.Role == stipulatorv1.BindingRole_BINDING_ROLE_UNSPECIFIED {
		return nil, fmt.Errorf("a role is required (implements, tests, or proves)")
	}
	if req.Symbol == "" || req.Backend == "" {
		return nil, fmt.Errorf("a backend and symbol are required")
	}
	if !knownBackends[req.Backend] {
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
	// The claim's identity — requirement, backend, symbol, role, and
	// the clause it already carries — is whole before the store is
	// searched for a twin (REQ-evidence-clause-claim).
	b := claim
	b.SetRequirementId(req.Requirement)
	b.SetBackend(req.Backend)
	b.SetSymbol(req.Symbol)
	b.SetRole(req.Role)
	for _, bf := range store.Bindings {
		for _, prior := range bf.Set.GetBindings() {
			if records.ClaimIdentity(target, prior) == records.ClaimIdentity(target, b) {
				return nil, fmt.Errorf("identical binding already exists in %s", bf.Path)
			}
		}
	}

	b.SetContentHash(contentHash)
	b.SetSourceHash(sourceHash)
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
		up, err := bind(over, backends, r)
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
func Unbind(fsys fs.FS, requirement, symbol string, role stipulatorv1.BindingRole, clause string) ([]Update, int, error) {
	store, err := records.Load(fsys)
	if err != nil {
		return nil, 0, err
	}
	// The clause narrows by the claim's own spelling, never by
	// resolution: unbind is the remedy for a claim whose clause the
	// corpus no longer declares, so it must reach a record the corpus
	// cannot resolve.
	clause = strings.TrimSpace(clause)
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
		if clause != "" && records.ClauseSpelling(b) != clause {
			return false
		}
		return true
	})
	if err != nil {
		return nil, 0, err
	}
	if removed == 0 {
		if clause != "" {
			// The clause narrows by spelling, and a claim may be spelled
			// by the other name of its clause: the refusal lists what is
			// recorded for the requirement and symbol so the operator
			// can name it — a dangling claim never reaches a binding
			// row, so no report can list it in its place.
			return nil, 0, fmt.Errorf("no binding matches %s + %s (%s) clause %s; the recorded claims there are %s", requirement, symbol, stipulatorv1.BindingRole_name[int32(role)], clause, recordedClaims(store, requirement, symbol))
		}
		return nil, 0, fmt.Errorf("no binding matches %s + %s (%s); verify view=bindings lists the recorded rows", requirement, symbol, stipulatorv1.BindingRole_name[int32(role)])
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

// recordedClaims lists the claims recorded for a requirement (and a
// symbol, when given) by role and clause spelling, for a refusal that
// must name what an unbind could have matched.
func recordedClaims(store *records.Store, requirement, symbol string) string {
	var out []string
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetRequirementId() != requirement || (symbol != "" && b.GetSymbol() != symbol) {
				continue
			}
			entry := b.GetSymbol() + " " + strings.ToLower(strings.TrimPrefix(b.GetRole().String(), "BINDING_ROLE_"))
			if sp := records.ClauseSpelling(b); sp != "" {
				entry += " clause " + sp
			} else {
				entry += " (whole requirement)"
			}
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}
