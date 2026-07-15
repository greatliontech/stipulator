package stipulate

import (
	"strings"
	"testing"
)

type recorder struct {
	testing.TB
	lines []string
}

func (r *recorder) Helper() {}
func (r *recorder) Log(args ...any) {
	for _, a := range args {
		r.lines = append(r.lines, a.(string))
	}
}

//gofresh:pure
func TestCoversEmitsMarker(t *testing.T) {
	r := &recorder{}
	Covers(r, "REQ-x-a", "REQ-x-b")
	if len(r.lines) != 2 {
		t.Fatalf("lines = %v", r.lines)
	}
	for i, id := range []string{"REQ-x-a", "REQ-x-b"} {
		if want := Marker + id; r.lines[i] != want {
			t.Fatalf("line[%d] = %q, want %q", i, r.lines[i], want)
		}
		if !strings.HasPrefix(r.lines[i], "stipulator:covers ") {
			t.Fatalf("marker drifted: %q", r.lines[i])
		}
	}
}
