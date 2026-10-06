//go:build solaris

package golang

// rlimitUnlimited reports the platform's RLIM_INFINITY, -3 on solaris
// and illumos — a value the uint64 field spells as all ones but two
// (the constant itself does not convert to the field's type).
func rlimitUnlimited[T ~int64 | ~uint64](v T) bool { return uint64(v) == ^uint64(2) }
