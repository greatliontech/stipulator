package initred

import "testing"

func init() {
	panic("init red")
}

func TestNeverRuns(t *testing.T) {}
