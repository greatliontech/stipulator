package records

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	stipulatorv1 "github.com/greatliontech/stipulator/gen/stipulator/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

// defaultHeader heads freshly created record files.
const defaultHeader = "# proto-file: proto/stipulator/v1/records.proto\n"

// AddBinding appends a binding to the named binding file (created with the
// default header when absent) and returns the file's new contents. The
// machine-owned rewrite rule applies: an existing file carrying comments
// outside its leading header is refused.
func AddBinding(store *Store, filePath string, b *stipulatorv1.Binding) ([]byte, error) {
	for _, bf := range store.Bindings {
		if bf.Path != filePath {
			continue
		}
		if line := commentOutsideHeader(bf.Raw); line > 0 {
			return nil, fmt.Errorf("%s:%d: comment outside the leading header block; move commentary to the commit message first", bf.Path, line)
		}
		bf.Set.SetBindings(append(bf.Set.GetBindings(), b))
		return renderBindingSet(bf), nil
	}
	set := &stipulatorv1.BindingSet{}
	set.SetBindings([]*stipulatorv1.Binding{b})
	return renderBindingSet(BindingFile{
		Path: filePath,
		Raw:  []byte(defaultHeader + "# proto-message: stipulator.v1.BindingSet\n"),
		Set:  set,
	}), nil
}

// RemoveBindings deletes every binding matched by fn across the store.
// Files left with no bindings are reported for deletion rather than written
// empty. The machine-owned rewrite rule applies to every touched file.
func RemoveBindings(store *Store, fn func(*stipulatorv1.Binding) bool) (updates map[string][]byte, deletions []string, removed int, err error) {
	updates = map[string][]byte{}
	for _, bf := range store.Bindings {
		var keep []*stipulatorv1.Binding
		matched := 0
		for _, b := range bf.Set.GetBindings() {
			if fn(b) {
				matched++
			} else {
				keep = append(keep, b)
			}
		}
		if matched == 0 {
			continue
		}
		if line := commentOutsideHeader(bf.Raw); line > 0 {
			return nil, nil, 0, fmt.Errorf("%s:%d: comment outside the leading header block; move commentary to the commit message first", bf.Path, line)
		}
		removed += matched
		if len(keep) == 0 {
			// Keep the in-memory store consistent with the deletion, or a
			// later add to the same path resurrects removed bindings.
			bf.Set.SetBindings(nil)
			deletions = append(deletions, bf.Path)
			continue
		}
		bf.Set.SetBindings(keep)
		updates[bf.Path] = renderBindingSet(bf)
	}
	return updates, deletions, removed, nil
}

// GapPath is the canonical file path for a requirement's gap record.
func GapPath(requirementID string) string {
	return path.Join(GapsDir, strings.TrimPrefix(strings.ToLower(requirementID), "req-")+".textproto")
}

// RenderGap renders a gap record deterministically with the standard
// header.
func RenderGap(g *stipulatorv1.Gap) []byte {
	var b strings.Builder
	b.WriteString(defaultHeader)
	b.WriteString("# proto-message: stipulator.v1.Gap\n\n")
	fmt.Fprintf(&b, "requirement_id: %s\n", strconv.Quote(g.GetRequirementId()))
	fmt.Fprintf(&b, "reason: %s\n", strconv.Quote(g.GetReason()))
	lc := g.GetLands()
	switch {
	case lc.HasCovered():
		fmt.Fprintf(&b, "lands { covered: %s }\n", strconv.Quote(lc.GetCovered()))
	case lc.HasExists():
		fmt.Fprintf(&b, "lands { exists: %s }\n", strconv.Quote(lc.GetExists()))
	case lc.HasManual():
		a := lc.GetManual()
		if a.GetFired() {
			fmt.Fprintf(&b, "lands { manual { condition: %s fired: true } }\n", strconv.Quote(a.GetCondition()))
		} else {
			fmt.Fprintf(&b, "lands { manual { condition: %s } }\n", strconv.Quote(a.GetCondition()))
		}
	}
	return []byte(b.String())
}

// Render re-renders one binding file through the machine-owned writer,
// refusing files carrying comments outside the leading header.
func Render(bf BindingFile) ([]byte, error) {
	if line := commentOutsideHeader(bf.Raw); line > 0 {
		return nil, fmt.Errorf("%s:%d: comment outside the leading header block; move commentary to the commit message first", bf.Path, line)
	}
	return renderBindingSet(bf), nil
}

// RenderTombstones renders the tombstone registry deterministically.
func RenderTombstones(retired []string) []byte {
	var b strings.Builder
	b.WriteString(defaultHeader)
	b.WriteString("# proto-message: stipulator.v1.Tombstones\n\n")
	for _, r := range retired {
		fmt.Fprintf(&b, "retired: %s\n", strconv.Quote(r))
	}
	return []byte(b.String())
}

// RemoveBindingsCollect is RemoveBindings that also hands back the removed
// bindings, for retargeting.
func RemoveBindingsCollect(store *Store, fn func(*stipulatorv1.Binding) bool, removedOut *[]*stipulatorv1.Binding) (map[string][]byte, []string, int, error) {
	collect := func(b *stipulatorv1.Binding) bool {
		if fn(b) {
			*removedOut = append(*removedOut, b)
			return true
		}
		return false
	}
	return RemoveBindings(store, collect)
}

// ParseBindingFile parses raw binding-file bytes into a BindingFile.
func ParseBindingFile(path string, raw []byte) (BindingFile, error) {
	set := &stipulatorv1.BindingSet{}
	if err := prototext.Unmarshal(raw, set); err != nil {
		return BindingFile{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return BindingFile{Path: path, Raw: raw, Set: set}, nil
}

// AttestationPath is the canonical file path for a requirement's
// attestation record.
func AttestationPath(requirementID string) string {
	return path.Join(AttestationsDir, strings.TrimPrefix(strings.ToLower(requirementID), "req-")+".textproto")
}

// RenderAttestations renders an attestation set deterministically with
// the standard header.
func RenderAttestations(set *stipulatorv1.AttestationSet) []byte {
	var b strings.Builder
	b.WriteString(defaultHeader)
	b.WriteString("# proto-message: stipulator.v1.AttestationSet\n")
	for _, a := range set.GetAttestations() {
		b.WriteString("\nattestations {\n")
		fmt.Fprintf(&b, "  requirement_id: %s\n", strconv.Quote(a.GetRequirementId()))
		fmt.Fprintf(&b, "  content_hash: %s\n", strconv.Quote(a.GetContentHash()))
		fmt.Fprintf(&b, "  reason: %s\n", strconv.Quote(a.GetReason()))
		b.WriteString("}\n")
	}
	return []byte(b.String())
}
