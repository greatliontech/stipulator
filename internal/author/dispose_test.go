package author

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/internal/compile"
	"github.com/greatliontech/stipulator/internal/records"
	"github.com/greatliontech/stipulator/stipulate"
	"google.golang.org/protobuf/encoding/prototext"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
)

// disposeFS builds a corpus whose records were authored against oldDoc,
// then swaps the spec to newDoc — the mid-disposition state.
func disposeFS(t *testing.T, oldDoc, newDoc string, extra map[string]string) fstest.MapFS {
	t.Helper()
	fsys := testFS(nil)
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(oldDoc)}
	// Author a pinned binding against the old text through the real verb.
	up, err := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F"))
	if err != nil {
		t.Fatal(err)
	}
	fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(newDoc)}
	for p, c := range extra {
		fsys[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return fsys
}

//gofresh:pure
func TestEditorial(t *testing.T) {
	stipulate.Covers(t, "REQ-change-editorial")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	newDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x, reworded.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	fsys := disposeFS(t, oldDoc, newDoc, nil)

	// A second stale binding file for the same requirement: re-pin spans
	// files and the update list is canonically ordered.
	fsys[".stipulator/bindings/zz-extra.textproto"] = &fstest.MapFile{Data: []byte(
		"bindings {\n  requirement_id: \"REQ-au-a\"\n  content_hash: \"" + strings.Repeat("0", 64) + "\"\n  backend: \"go\"\n  symbol: \"example.com/p.G\"\n  role: BINDING_ROLE_TESTS\n}\n")}
	ups, _, err := Editorial(fsys, "REQ-au-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 {
		t.Fatalf("updates = %d", len(ups))
	}
	if !(ups[0].Path < ups[1].Path) {
		t.Fatal("updates not canonically ordered")
	}
	set := &stipulatorv1.BindingSet{}
	if err := prototext.Unmarshal(ups[0].Content, set); err != nil {
		t.Fatal(err)
	}
	// Re-pinned to the corpus's current hash, no other change.
	spec, err := compileClean(fsys)
	if err != nil {
		t.Fatal(err)
	}
	current := ""
	for _, r := range spec.GetRequirements() {
		if r.GetId() == "REQ-au-a" {
			current = r.GetContentHash()
		}
	}
	if set.GetBindings()[0].GetContentHash() != current {
		t.Fatal("editorial did not re-pin to the current hash")
	}

	if _, _, err := Editorial(fsys, "REQ-au-b"); err == nil {
		t.Fatal("editorial with nothing stale succeeded")
	}
	if _, _, err := Editorial(fsys, "REQ-au-ghost"); err == nil {
		t.Fatal("unknown requirement accepted")
	}

	// Error arms propagate for both a corpus that no longer compiles and a
	// broken store.
	fsys["specs/broken.md"] = &fstest.MapFile{Data: []byte("# B\n\n**REQ-au-a** (behavior): Redeclared, it MUST clash.\n")}
	if _, _, err := Editorial(fsys, "REQ-au-a"); err == nil {
		t.Fatal("non-compiling corpus swallowed")
	}
	delete(fsys, "specs/broken.md")
	fsys[".stipulator/bindings/broken.textproto"] = &fstest.MapFile{Data: []byte("not textproto {{{")}
	if _, _, err := Editorial(fsys, "REQ-au-a"); err == nil {
		t.Fatal("broken store swallowed")
	}
}

//gofresh:pure
func TestRetire(t *testing.T) {
	stipulate.Covers(t, "REQ-change-retire")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	newDoc := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n" // a removed
	fsys := disposeFS(t, oldDoc, newDoc, map[string]string{
		".stipulator/gaps/au-a.textproto": "requirement_id: \"REQ-au-a\"\nreason: \"r\"\nlands { manual { condition: \"x\" } }\n",
	})

	ups, err := Retire(fsys, "REQ-au-a", false)
	if err != nil {
		t.Fatal(err)
	}
	var tombstones []byte
	deletedGap, deletedBindings := false, false
	for _, up := range ups {
		switch {
		case up.Path == records.TombstonesPath:
			tombstones = up.Content
		case up.Path == ".stipulator/gaps/au-a.textproto" && up.Content == nil:
			deletedGap = true
		case strings.HasPrefix(up.Path, ".stipulator/bindings/") && up.Content == nil:
			deletedBindings = true
		}
	}
	if !strings.Contains(string(tombstones), `retired: "REQ-au-a"`) {
		t.Fatalf("tombstone missing:\n%s", tombstones)
	}
	if !deletedGap || !deletedBindings {
		t.Fatalf("gap deleted=%v bindings deleted=%v", deletedGap, deletedBindings)
	}

	t.Run("still declared refuses", func(t *testing.T) {
		fsys2 := disposeFS(t, oldDoc, oldDoc, nil)
		if _, err := Retire(fsys2, "REQ-au-a", false); err == nil || !strings.Contains(err.Error(), "still declared") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("dangling reference refuses", func(t *testing.T) {
		ref := "# T\n\n**REQ-au-b** (behavior): Like REQ-au-a it MUST y.\n"
		fsys2 := disposeFS(t, oldDoc, ref, nil)
		if _, err := Retire(fsys2, "REQ-au-a", false); err == nil || !strings.Contains(err.Error(), "does not compile") {
			t.Fatalf("err = %v", err)
		}
	})
}

//gofresh:pure
func TestSupersede(t *testing.T) {
	stipulate.Covers(t, "REQ-change-split-merge", "REQ-change-transient")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	split := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n\n" +
		"**REQ-au-a1** (behavior, supersedes REQ-au-a): First half, it MUST x1.\n\n" +
		"**REQ-au-a2** (behavior, supersedes REQ-au-a): Second half, it MUST x2.\n"
	fsys := disposeFS(t, oldDoc, split, nil)

	ups, err := Supersede(fsys, []string{"REQ-au-a"}, []string{"REQ-au-a1", "REQ-au-a2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// The only persistent effects: tombstones + record rewrites. Collect
	// retargeted bindings and verify stale-by-contract (no content pin).
	retargeted := map[string]bool{}
	for _, up := range ups {
		if up.Content == nil || !strings.HasPrefix(up.Path, ".stipulator/bindings/") {
			continue
		}
		set := &stipulatorv1.BindingSet{}
		if err := prototext.Unmarshal(up.Content, set); err != nil {
			t.Fatal(err)
		}
		for _, b := range set.GetBindings() {
			retargeted[b.GetRequirementId()] = true
			if b.GetContentHash() != "" {
				t.Fatalf("retargeted binding born pinned (must be stale): %v", b)
			}
			if b.GetSymbol() != "example.com/p.F" {
				t.Fatalf("symbol lost in retarget: %v", b)
			}
		}
	}
	if !retargeted["REQ-au-a1"] || !retargeted["REQ-au-a2"] {
		t.Fatalf("bindings not retargeted to both successors: %v", retargeted)
	}
	for _, up := range ups {
		if !strings.HasPrefix(up.Path, ".stipulator/") {
			t.Fatalf("disposition wrote outside the record stores: %s", up.Path)
		}
	}

	t.Run("missing supersedes clause refuses", func(t *testing.T) {
		noEdge := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n\n" +
			"**REQ-au-a1** (behavior): It MUST x1.\n"
		fsys2 := disposeFS(t, oldDoc, noEdge, nil)
		_, err := Supersede(fsys2, []string{"REQ-au-a"}, []string{"REQ-au-a1"}, false)
		if err == nil || !strings.Contains(err.Error(), "declares `supersedes` for none of REQ-au-a") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown successor refuses", func(t *testing.T) {
		fsys2 := disposeFS(t, oldDoc, split, nil)
		if _, err := Supersede(fsys2, []string{"REQ-au-a"}, []string{"REQ-au-ghost"}, false); err == nil {
			t.Fatal("unknown successor accepted")
		}
	})
}

// An editorial re-pin names the clause each re-consented clause claim
// now denotes — an ordinal moved by the edit is visible in the
// response — and refuses when a clause claim no longer resolves, so
// consent to a dangling claim is never recorded
// (REQ-evidence-clause-claim, REQ-change-editorial).
//
//gofresh:pure
func TestEditorialNamesTheClauseEachClaimNowDenotes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-clause-claim")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST hold:\n\n- **alpha** first\n- second\n\n**REQ-au-b** (behavior): It MUST y.\n"
	// The edit inserts an item above the second: ordinal 2 now denotes
	// the inserted text.
	newDoc := "# T\n\n**REQ-au-a** (behavior): It MUST hold:\n\n- **alpha** first\n- inserted\n- second\n\n**REQ-au-b** (behavior): It MUST y.\n"
	fsys := testFS(nil)
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(oldDoc)}
	for _, clause := range []string{"alpha", "2"} {
		r := bindReq("REQ-au-a", "example.com/p.F")
		r.Role, r.Clause = stipulatorv1.BindingRole_BINDING_ROLE_TESTS, clause
		up, err := Bind(fsys, backends, r)
		if err != nil {
			t.Fatal(err)
		}
		fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	}
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(newDoc)}
	ups, consented, err := Editorial(fsys, "REQ-au-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 {
		t.Fatalf("updates = %d", len(ups))
	}
	want := []string{
		"example.com/p.F now claims clause 1 `alpha` (alpha first)",
		"example.com/p.F now claims clause 2 (inserted)",
	}
	if strings.Join(consented, "\n") != strings.Join(want, "\n") {
		t.Fatalf("consented =\n%s\nwant\n%s", strings.Join(consented, "\n"), strings.Join(want, "\n"))
	}
	// The label is removed: its claim dangles, and the re-pin refuses
	// before writing anything.
	fsys["specs/a.md"] = &fstest.MapFile{Data: []byte(strings.Replace(newDoc, "**alpha** first", "first, unlabeled", 1))}
	if _, _, err := Editorial(fsys, "REQ-au-a"); err == nil || !strings.Contains(err.Error(), "names clause `alpha`, which REQ-au-a no longer declares") {
		t.Fatalf("dangling clause claim re-consented: %v", err)
	}
}

// Every authoring verb stamps the consent-source pin beside the content
// pin, and the named re-pin over a rehashed record names it as such —
// the operator asked to re-consent and learns there was nothing to
// consent to for that record (REQ-evidence-consent-current,
// REQ-change-editorial).
//
//gofresh:pure
func TestAuthoringStampsTheSourcePinAndEditorialNamesRehashes(t *testing.T) {
	stipulate.Covers(t, "REQ-evidence-consent-current")
	fsys := attestableFS(nil)
	spec, err := compileClean(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var source string
	for _, r := range spec.GetRequirements() {
		if r.GetId() == "REQ-au-a" {
			source = r.GetSourceHash()
		}
	}
	up, err := Bind(fsys, backends, bindReq("REQ-au-a", "example.com/p.F"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(up.Content), "source_hash: \""+source+"\"") {
		t.Fatalf("bind did not stamp the source pin:\n%s", up.Content)
	}
	g := &stipulatorv1.Gap{}
	g.SetRequirementId("REQ-au-b")
	g.SetReason("r")
	lands, err := NewLandingCondition("", "", "c", false)
	if err != nil {
		t.Fatal(err)
	}
	g.SetLands(lands)
	gup, _, _, err := Gap(fsys, g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gup.Content), "source_hash: \"") {
		t.Fatalf("gap did not stamp the source pin:\n%s", gup.Content)
	}
	aup, _, err := AttestRequirement(fsys, "REQ-au-s", "judged")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(aup.Content), "source_hash: \"") {
		t.Fatalf("attest did not stamp the source pin:\n%s", aup.Content)
	}
	// A rehashed binding under the named re-pin: re-pinned and named.
	fsys[".stipulator/bindings/au.textproto"] = &fstest.MapFile{Data: []byte(
		"bindings {\n  requirement_id: \"REQ-au-a\"\n  content_hash: \"" + strings.Repeat("0", 64) + "\"\n  source_hash: \"" + source + "\"\n  backend: \"go\"\n  symbol: \"example.com/p.F\"\n  role: BINDING_ROLE_IMPLEMENTS\n}\n")}
	ups, consented, err := Editorial(fsys, "REQ-au-a")
	if err != nil || len(ups) != 1 {
		t.Fatalf("editorial over a rehash: %v %v", ups, err)
	}
	if len(consented) != 1 || consented[0] != "example.com/p.F rehashed — "+records.RehashNote {
		t.Fatalf("consented = %v, want the rehash named", consented)
	}
}

// The named re-pin's no-op states a fact: "text unchanged" only when
// records consent to the current text, "no records" when none name the
// requirement, and the re-attest ceremony when the only consent not
// holding is an attestation's — a judgment the editorial re-pin never
// rewrites (REQ-pin-backfill).
//
//gofresh:pure
func TestEditorialNoOpNamesItsReason(t *testing.T) {
	stipulate.Covers(t, "REQ-pin-backfill")
	fsys := attestableFS(nil)
	_, _, err := Editorial(fsys, "REQ-au-b")
	if !errors.Is(err, ErrNothingStale) || NoOpNote(err) != "no records name it; nothing to re-consent" {
		t.Fatalf("no records: err=%v note=%q", err, NoOpNote(err))
	}
	up, err := Bind(fsys, backends, bindReq("REQ-au-b", "example.com/p.F"))
	if err != nil {
		t.Fatal(err)
	}
	fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	_, _, err = Editorial(fsys, "REQ-au-b")
	if !errors.Is(err, ErrNothingStale) || NoOpNote(err) != "text unchanged; nothing to re-consent" {
		t.Fatalf("current binding: err=%v note=%q", err, NoOpNote(err))
	}
	// An attestation vouched for other text: the editorial re-pin does
	// not rewrite it, and the note says which ceremony does.
	aup, _, err := AttestRequirement(fsys, "REQ-au-s", "judged")
	if err != nil {
		t.Fatal(err)
	}
	fsys[aup.Path] = &fstest.MapFile{Data: []byte(strings.Replace(string(aup.Content), "content_hash: \"", "content_hash: \"0", 1))}
	fsys[aup.Path] = &fstest.MapFile{Data: []byte(strings.Replace(string(fsys[aup.Path].Data), "source_hash: \"", "source_hash: \"0", 1))}
	_, _, err = Editorial(fsys, "REQ-au-s")
	if !errors.Is(err, ErrNothingStale) || !strings.Contains(NoOpNote(err), "re-attest: stipulator attest requirement --req REQ-au-s") {
		t.Fatalf("stale attestation only: err=%v note=%q", err, NoOpNote(err))
	}
}

// The removed-source supersede is one step: on the corpus as edited —
// the source gone, the successor declaring — the base does not compile
// (its refusal names this disposition), and the disposition still
// tombstones the source, accepts the edge, and retargets the source's
// binding; a source no record names needs force, the typo guard
// (REQ-change-split-merge).
//
//gofresh:pure
func TestSupersedeConsumesTheMidDispositionCorpus(t *testing.T) {
	stipulate.Covers(t, "REQ-change-split-merge")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-b** (behavior): It MUST y.\n"
	rewritten := "# T\n\n**REQ-au-b** (behavior): It MUST y.\n\n**REQ-au-n** (behavior, supersedes REQ-au-a): It MUST x, rewritten.\n"
	fsys := disposeFS(t, oldDoc, rewritten, nil)
	// Every authoring verb's refusal on the base carries the fault AND
	// the one-step remedy — the consumer met the fault on a gap declare
	// and had nowhere to look — while the remedy is its own diagnostic
	// class, never counted as an error.
	if _, err := compileClean(fsys); err == nil || !strings.Contains(err.Error(), "supersedes REQ-au-a, which is neither declared nor tombstoned; remedy: if REQ-au-a was removed by this edit") || !strings.Contains(err.Error(), "stipulator dispose supersede --from REQ-au-a --into REQ-au-n") {
		t.Fatalf("mid-disposition base: %v, want the fault with its remedy", err)
	}
	if !remedyNamed(t, fsys, "stipulator dispose supersede --from REQ-au-a --into REQ-au-n") {
		t.Fatal("the base's diagnostics do not name the one-step disposition")
	}
	ups, err := Supersede(fsys, []string{"REQ-au-a"}, []string{"REQ-au-n"}, false)
	if err != nil {
		t.Fatalf("one-step supersede over a non-compiling base: %v", err)
	}
	for _, up := range ups {
		fsys[up.Path] = &fstest.MapFile{Data: up.Content}
		if up.Content == nil {
			delete(fsys, up.Path)
		}
	}
	spec, err := compileClean(fsys)
	if err != nil {
		t.Fatalf("after the disposition the corpus must compile: %v", err)
	}
	edges := 0
	for _, e := range spec.GetEdges() {
		if e.GetKind() == stipulatorv1.EdgeKind_EDGE_KIND_SUPERSEDES && e.GetTo().GetRequirementId() == "REQ-au-a" {
			edges++
		}
	}
	if edges != 1 {
		t.Fatalf("supersedes edge to the tombstoned source = %d, want 1", edges)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(store.Tombstones, "REQ-au-a") {
		t.Fatalf("source not tombstoned: %v", store.Tombstones)
	}
	retargeted := false
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if b.GetRequirementId() == "REQ-au-n" && b.GetSymbol() == "example.com/p.F" && b.GetContentHash() == "" {
				retargeted = true
			}
		}
	}
	if !retargeted {
		t.Fatal("the source's binding was not retargeted stale to the successor")
	}
	// An unrecorded source: the typo guard names force.
	unrecorded := disposeFS(t, oldDoc, "# T\n\n**REQ-au-a** (behavior): It MUST x.\n\n**REQ-au-m** (behavior, supersedes REQ-au-b): It MUST y, rewritten.\n", nil)
	if _, err := Supersede(unrecorded, []string{"REQ-au-b"}, []string{"REQ-au-m"}, false); err == nil || !strings.Contains(err.Error(), "requires --force") {
		t.Fatalf("unrecorded source without force: %v", err)
	}
	if _, err := Supersede(unrecorded, []string{"REQ-au-b"}, []string{"REQ-au-m"}, true); err != nil {
		t.Fatalf("unrecorded source with force: %v", err)
	}
}

// The disposition's unit is the connected component and its bindings
// follow the declared edges: in a chain — c supersedes a and b, d
// supersedes b — one call tombstones both sources, a's binding
// retargets to c alone, b's to c and d, and no binding lands on a
// successor that does not declare its source; a source no named
// successor declares refuses naming it (REQ-change-split-merge).
//
//gofresh:pure
func TestSupersedeFollowsTheDeclaredEdges(t *testing.T) {
	stipulate.Covers(t, "REQ-change-split-merge")
	oldDoc := "# T\n\n**REQ-au-a** (behavior): It MUST a.\n\n**REQ-au-b** (behavior): It MUST b.\n"
	chain := "# T\n\n**REQ-au-c** (behavior, supersedes REQ-au-a REQ-au-b): It MUST c.\n\n**REQ-au-d** (behavior, supersedes REQ-au-b): It MUST d.\n"
	fsys := disposeFS(t, oldDoc, chain, map[string]string{
		".stipulator/bindings/b.textproto": "bindings {\n  requirement_id: \"REQ-au-b\"\n  backend: \"go\"\n  symbol: \"example.com/p.G\"\n  role: BINDING_ROLE_TESTS\n}\n",
	})
	// The remedy on the mid-disposition base spells the whole component.
	if !remedyNamed(t, fsys, "--from REQ-au-a,REQ-au-b --into REQ-au-c,REQ-au-d") {
		t.Fatal("the base's diagnostics do not spell the whole component")
	}
	// A partial invocation refuses quoting the FAULT the overlay still
	// carries — never a remedy computed for the overlay's hypothetical
	// state, which would name a call that cannot work from the base.
	if _, err := Supersede(fsys, []string{"REQ-au-a"}, []string{"REQ-au-c"}, false); err == nil || !strings.Contains(err.Error(), "supersedes REQ-au-b, which is neither declared nor tombstoned") || strings.Contains(err.Error(), "dispose supersede") {
		t.Fatalf("partial invocation refusal: %v, want the fault alone", err)
	}
	ups, err := Supersede(fsys, []string{"REQ-au-a", "REQ-au-b"}, []string{"REQ-au-c", "REQ-au-d"}, false)
	if err != nil {
		t.Fatalf("one call over a chain: %v", err)
	}
	for _, up := range ups {
		if up.Content == nil {
			delete(fsys, up.Path)
			continue
		}
		fsys[up.Path] = &fstest.MapFile{Data: up.Content}
	}
	if _, err := compileClean(fsys); err != nil {
		t.Fatalf("after the disposition: %v", err)
	}
	store, err := records.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			got[b.GetRequirementId()] = append(got[b.GetRequirementId()], b.GetSymbol())
		}
	}
	for id := range got {
		sort.Strings(got[id])
	}
	want := map[string][]string{
		"REQ-au-c": {"example.com/p.F", "example.com/p.G"}, // declares a and b
		"REQ-au-d": {"example.com/p.G"},                    // declares b only
	}
	for id, syms := range want {
		if !slices.Equal(got[id], syms) {
			t.Fatalf("bindings on %s = %v, want %v (retarget follows the declared edges); all: %v", id, got[id], syms, got)
		}
	}
	if _, has := got["REQ-au-a"]; has {
		t.Fatalf("the source kept bindings: %v", got)
	}
	// A named source no named successor declares refuses, naming it.
	if _, err := Supersede(disposeFS(t, oldDoc, chain, nil), []string{"REQ-au-a", "REQ-au-b"}, []string{"REQ-au-d"}, true); err == nil || !strings.Contains(err.Error(), "no named successor declares `supersedes REQ-au-a`") {
		t.Fatalf("undeclared source: %v", err)
	}
}

// remedyNamed reports whether compiling fsys yields a remedy diagnostic
// containing text.
func remedyNamed(t *testing.T, fsys fstest.MapFS, text string) bool {
	t.Helper()
	_, diags, err := compile.Compile(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Remedy && strings.Contains(d.Message, text) {
			return true
		}
	}
	return false
}
