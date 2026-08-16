module example.com/fixture

go 1.26

// The rapid dependency is a hermetic in-tree stub: the classifier fixture
// needs the import path to type-check, never the real library.
require (
	github.com/leanovate/gopter v0.0.0
	pgregory.net/rapid v0.0.0
)

replace pgregory.net/rapid => ./rapidstub

replace github.com/leanovate/gopter => ./gopterstub
