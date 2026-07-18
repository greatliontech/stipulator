package fuzzseed

import "testing"

func FuzzRefuse(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		if Refuses(s) {
			t.Fatal("committed seed fails replay")
		}
	})
}
