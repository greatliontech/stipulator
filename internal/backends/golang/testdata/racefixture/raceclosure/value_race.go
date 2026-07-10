//go:build race

package raceclosure

func selectedValue() string { return "race-v1" }
