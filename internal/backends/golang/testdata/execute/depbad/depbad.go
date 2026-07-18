// Package depbad compiles on its own but imports a dependency that does
// not.
package depbad

import _ "example.com/exec/builderr"

// V is exported.
var V = 1
