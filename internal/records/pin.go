package records

import (
	"fmt"
	"strconv"
	"strings"
)

// ShapeKey keys the shape-hash map passed to Pin.
func ShapeKey(backend, symbol string) string { return backend + "|" + symbol }

// Pin backfills: it sets only UNSET content pins (to the requirement's
// current hash) and shape pins (set or differing — shapes come from
// resolution and cannot lie). A differing content pin is never rewritten:
// that is an editorial disposition, and staleness must not be laundered by
// a blanket re-pin. Unknown requirements are left untouched — reporting
// them is the verifier's job. Files whose pins are all current are omitted
// from the result.
//
// Output is rendered by hand rather than through prototext.Marshal: the
// protobuf-go text marshaler deliberately randomizes its whitespace, and
// pin output is observable state that determinism rules over. The leading
// comment header of each file (its '#' lines) is preserved.
func Pin(store *Store, hashes, shapes map[string]string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, bf := range store.Bindings {
		changed := false
		for _, b := range bf.Set.GetBindings() {
			h, ok := hashes[b.GetRequirementId()]
			if ok && b.GetContentHash() == "" {
				b.SetContentHash(h)
				changed = true
			}
			s, ok := shapes[ShapeKey(b.GetBackend(), b.GetSymbol())]
			if ok && b.GetShapeHash() != s {
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
		if line := commentOutsideHeader(bf.Raw); line > 0 {
			return nil, fmt.Errorf("%s:%d: comment outside the leading header block; move commentary to the commit message before pinning", bf.Path, line)
		}
		out[bf.Path] = renderBindingSet(bf)
	}
	return out, nil
}

// commentOutsideHeader returns the 1-based line of the first comment after
// the leading header block, or 0.
func commentOutsideHeader(raw []byte) int {
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
		// Match commentOutsideHeader's notion of a header line exactly, or
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
