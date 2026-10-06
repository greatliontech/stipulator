//go:build linux

package golang

// rlimitUnlimited reports the platform's RLIM_INFINITY, -1 on linux —
// a value the uint64 field spells as all ones (the constant itself
// does not convert to the field's type).
func rlimitUnlimited[T ~int64 | ~uint64](v T) bool { return uint64(v) == ^uint64(0) }
