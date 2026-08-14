# GOOS/GOARCH and race build dimensions are invisible to discovery and resolution

Resolution's build-selection views span the invocation tag-set
dimension (REQ-go-build-selections); the policy also declares
per-invocation GOOS/GOARCH, and `race: true` sets the implicit `race`
build tag at build time. A `//go:build race` or goos-gated declaration
resolves in no view - and symmetrically, execution discovery's
`go list` passes only `-tags` (no `-race`, host GOOS), so such tests
are equally invisible to discovery: the gap is dimension-wide and
pre-existing, not resolution-local. A fix extends both discovery and
the resolution views with the race tag and (for on-host-executable
selections only) the GOOS/GOARCH pair; a cross-GOOS invocation cannot
execute on-host, so its resolution-only view would bind witnesses no
run can grant - that half needs its own design.

Lands: a policy declares a race-gated or goos-gated test the corpus
must bind.
