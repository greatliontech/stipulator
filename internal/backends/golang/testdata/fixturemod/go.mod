module example.com/fixture

go 1.26

// The rapid dependency is a hermetic in-tree stub: the classifier fixture
// needs the import path to type-check, never the real library.
require pgregory.net/rapid v0.0.0

replace pgregory.net/rapid => ./rapidstub
