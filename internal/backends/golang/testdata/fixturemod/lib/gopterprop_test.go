package lib

import (
	"testing"

	"github.com/leanovate/gopter"
)

func TestGopterProp(t *testing.T) {
	properties := gopter.NewProperties(gopter.DefaultTestParameters())
	properties.Property("stable", func() bool { return Add(1, 1) == 2 })
	properties.TestingRun(t)
}

func TestGopterRegistrationOnly(t *testing.T) {
	properties := gopter.NewProperties(gopter.DefaultTestParameters())
	properties.Property("registered but never driven", func() bool { return true })
	_ = properties
}
