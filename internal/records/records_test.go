package records

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/greatliontech/stipulator/stipulate"
)

// Hidden files are never records: an applier's leaked staging temp or
// an editor dropping must not brick the load, while a visible stray
// non-.textproto file stays the loud typo guard (REQ-record-cas's
// staging rides on this tolerance).
//
//gofresh:pure
func TestLoadSkipsHiddenFiles(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	fsys := fstest.MapFS{
		".stipulator/gaps/a.textproto":          {Data: []byte("requirement_id: \"REQ-r-a\"\nreason: \"r\"\nlands { manual { condition: \"c\" } }\n")},
		".stipulator/gaps/.stipulator-apply-42": {Data: []byte("half-staged")},
	}
	store, err := Load(fsys)
	if err != nil {
		t.Fatalf("hidden file bricked the load: %v", err)
	}
	if len(store.Gaps) != 1 {
		t.Fatalf("gaps = %d", len(store.Gaps))
	}
	fsys[".stipulator/gaps/stray.txt"] = &fstest.MapFile{Data: []byte("x")}
	if _, err := Load(fsys); err == nil {
		t.Fatal("visible stray file accepted")
	}
}

// An empty record file is never a record: no kind is written empty (an
// emptied set is deleted), so an empty file at a record name is the
// residue of a write that died between its reservation and its rename,
// and the load names it for the operator rather than reading a record
// with no content — an empty gap would otherwise load as a record
// naming no requirement and vanish from every surface (REQ-record-cas).
func TestLoadRefusesAnEmptyRecordFile(t *testing.T) {
	stipulate.Covers(t, "REQ-record-cas")
	for _, p := range []string{".stipulator/gaps/g.textproto", ".stipulator/bindings/b.textproto", ".stipulator/attestations/a.textproto", TombstonesPath} {
		fsys := fstest.MapFS{p: {Data: nil}}
		_, err := Load(fsys)
		if p == TombstonesPath {
			_, err = LoadTombstones(fsys)
		}
		if err == nil || !strings.Contains(err.Error(), p+" is empty") {
			t.Fatalf("empty %s loaded: %v; want the reservation residue named", p, err)
		}
	}
}
