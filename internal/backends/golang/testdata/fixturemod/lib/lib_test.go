package lib

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("sum")
	}
	if Add(0, 5) != 5 {
		t.Fatal("zero arm")
	}
}

func TestWeak(t *testing.T) {
	if Weak(5) != 5 {
		t.Fatal("small arm")
	}
}

// TestVacuous is deliberately assertion-free: the vacuity check's fixture.
func TestVacuous(t *testing.T) {
	_ = Add(1, 2)
}
