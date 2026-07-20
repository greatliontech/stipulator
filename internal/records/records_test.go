package records

import (
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
