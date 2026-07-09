// Package methods exercises the staged-surface method and generic-receiver
// symbol keying.
package methods

// Counter has both pointer- and value-receiver methods.
type Counter struct{ n int }

func (c *Counter) Inc()      { c.n++ }
func (c Counter) Value() int { return c.n }

// Box has a generic receiver, so its method keys must strip the type
// parameters.
type Box[T any] struct{ v T }

func (b *Box[T]) Get() T { return b.v }
