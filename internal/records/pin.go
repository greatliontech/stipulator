package records

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ShapeKey keys the shape-hash map passed to Pin.
func ShapeKey(backend, symbol string) string { return backend + "|" + symbol }

// Pin backfills: it sets only UNSET content pins (to the requirement's
// current hash) and shape pins (set or differing — shapes come from
// resolution and cannot lie). A differing content pin is never rewritten:
// that is an editorial disposition, and staleness must not be laundered by
// a blanket re-pin. The requirements whose differing pins were preserved
// are returned (sorted, deduplicated) so every surface can name what
// awaits editorial re-consent (REQ-pin-backfill), and the symbols whose
// DIFFERING shape pins were rewritten are returned beside them — a
// rewritten shape pin clears verify's shape-mismatch signal, the one
// trace that a bound implementation moved, so the surfaces must name it
// rather than clear it invisibly (a backfilled unset shape pin is not
// reported: nothing was cleared). Gap records ride the same discipline:
// an unset gap pin backfills, a differing one is preserved and named
// beside the bindings' (REQ-gap-consent). Unknown requirements
// are left untouched — reporting them is the verifier's job. Files whose
// pins are all current are omitted from the result.
//
// Output is rendered by hand rather than through prototext.Marshal: the
// protobuf-go text marshaler deliberately randomizes its whitespace, and
// pin output is observable state that determinism rules over. The leading
// comment header of each file (its '#' lines) is preserved.
func Pin(store *Store, hashes, shapes map[string]string) (map[string][]byte, []string, []string, error) {
	out := map[string][]byte{}
	preservedSet := map[string]bool{}
	reshapedSet := map[string]bool{}
	for _, bf := range store.Bindings {
		changed := false
		for _, b := range bf.Set.GetBindings() {
			h, ok := hashes[b.GetRequirementId()]
			switch {
			case ok && b.GetContentHash() == "":
				b.SetContentHash(h)
				changed = true
			case ok && b.GetContentHash() != h:
				preservedSet[b.GetRequirementId()] = true
			}
			s, ok := shapes[ShapeKey(b.GetBackend(), b.GetSymbol())]
			if ok && b.GetShapeHash() != s {
				if b.GetShapeHash() != "" {
					reshapedSet[b.GetSymbol()] = true
				}
				b.SetShapeHash(s)
				changed = true
			}
		}
		if !changed {
			continue
		}
		// Binding files are machine-owned: rewriting would destroy any
		// commentary outside the leading header, so refuse instead of
		// silently dropping it.
		if line := CommentOutsideHeader(bf.Raw); line > 0 {
			return nil, nil, nil, fmt.Errorf("%s:%d: comment outside the leading header block; move commentary to the commit message before pinning", bf.Path, line)
		}
		out[bf.Path] = renderBindingSet(bf)
	}
	// Gap records ride the same blanket discipline: an unset pin
	// backfills to the current hash (a pre-field record gains its
	// consent surface over text nobody edited), while a differing pin
	// is a consent question the blanket form never answers — preserved
	// and named, exactly as a binding's (REQ-gap-consent).
	for _, gf := range store.Gaps {
		h, ok := hashes[gf.Gap.GetRequirementId()]
		if !ok {
			continue
		}
		switch {
		case gf.Gap.GetContentHash() == "":
			gf.Gap.SetContentHash(h)
			content, err := RenderGapFile(gf)
			if err != nil {
				return nil, nil, nil, err
			}
			out[gf.Path] = content
		case gf.Gap.GetContentHash() != h:
			preservedSet[gf.Gap.GetRequirementId()] = true
		}
	}
	preserved := make([]string, 0, len(preservedSet))
	for id := range preservedSet {
		preserved = append(preserved, id)
	}
	sort.Strings(preserved)
	reshaped := make([]string, 0, len(reshapedSet))
	for sym := range reshapedSet {
		reshaped = append(reshaped, sym)
	}
	sort.Strings(reshaped)
	return out, preserved, reshaped, nil
}

// ShapeMismatched reports, per requirement id, the bound symbols whose
// resolved shape differs from the stored pin — the mismatch the ids
// (editorial) form does not fix, so its surfaces can say so instead of
// answering "pins current" while the gate stays red. Ids with no
// mismatching binding are absent from the result; symbols per id are
// sorted.
func ShapeMismatched(store *Store, ids []string, shapes map[string]string) map[string][]string {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	perID := map[string]map[string]bool{}
	for _, bf := range store.Bindings {
		for _, b := range bf.Set.GetBindings() {
			if !wanted[b.GetRequirementId()] {
				continue
			}
			s, ok := shapes[ShapeKey(b.GetBackend(), b.GetSymbol())]
			if ok && b.GetShapeHash() != "" && b.GetShapeHash() != s {
				if perID[b.GetRequirementId()] == nil {
					perID[b.GetRequirementId()] = map[string]bool{}
				}
				perID[b.GetRequirementId()][b.GetSymbol()] = true
			}
		}
	}
	res := map[string][]string{}
	for id, syms := range perID {
		list := make([]string, 0, len(syms))
		for sym := range syms {
			list = append(list, sym)
		}
		sort.Strings(list)
		res[id] = list
	}
	return res
}

// CommentOutsideHeader returns the 1-based line of the first comment after
// the leading header block, or 0 — the machine-owned-record test every
// tool rewrite of a binding or gap file runs before destroying commentary
// (REQ-evidence-binding-machine-owned).
func CommentOutsideHeader(raw []byte) int {
	inHeader := true
	for i, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if inHeader {
			if strings.HasPrefix(t, "#") {
				continue
			}
			inHeader = false
		}
		if strings.HasPrefix(t, "#") {
			return i + 1
		}
	}
	return 0
}

func renderBindingSet(bf BindingFile) []byte {
	var b strings.Builder
	for _, line := range strings.Split(string(bf.Raw), "\n") {
		// Match CommentOutsideHeader's notion of a header line exactly, or
		// an indented header comment would silently vanish on re-render.
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, bind := range bf.Set.GetBindings() {
		b.WriteString("\nbindings {\n")
		writeField(&b, "requirement_id", bind.GetRequirementId())
		writeField(&b, "content_hash", bind.GetContentHash())
		writeField(&b, "backend", bind.GetBackend())
		writeField(&b, "symbol", bind.GetSymbol())
		fmt.Fprintf(&b, "  role: %s\n", bind.GetRole())
		writeField(&b, "shape_hash", bind.GetShapeHash())
		b.WriteString("}\n")
	}
	return []byte(b.String())
}

func writeField(b *strings.Builder, name, value string) {
	if value != "" {
		fmt.Fprintf(b, "  %s: %s\n", name, strconv.Quote(value))
	}
}
