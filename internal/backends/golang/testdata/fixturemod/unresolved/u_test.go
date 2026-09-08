package unresolved

import (
	"testing"

	"example.com/fixture/taggedhelper"
	"pgregory.net/rapid"
)

func TestViaTagged(t *testing.T) {
	taggedhelper.Run(t, func(rt *rapid.T) {
		if Add(1, 1) != 2 {
			rt.Fatal("broken")
		}
	})
}
