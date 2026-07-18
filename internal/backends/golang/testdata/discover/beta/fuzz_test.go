package beta

import "testing"

func FuzzBeta(f *testing.F) {
	f.Add(2)
	f.Fuzz(func(t *testing.T, x int) {
		if Double(x) != x+x {
			t.Fatal("double drifted")
		}
	})
}
