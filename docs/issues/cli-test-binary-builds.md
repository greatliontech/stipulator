# Eight CLI build sites duplicate harness preparation

Lands: tooling-redesign 5.6

`internal/cmd` now has eight CLI-binary build sites: check, bind, two pin
sites, scoped prune, gap lifecycle, interruption and version tests. The
original seven-build description is stale. These sites independently invoke
`go build` into temporary directories. Four `neutralAmbient` helpers across
the test packages also carry parallel ambient-control setup.

Share a once-built CLI artifact where build identity and environment are the
same, retaining each test's fixture and process isolation. Reconcile the four
ambient helpers by actual policy and scope rather than assuming identical
names mean interchangeable setup. Preserve in-process command pins where
mutation overlays cannot reach a separately built binary, and preserve
version-query tests' metadata and outside-project behavior. Measure the same
test population before and after; skipped cases are not saved work.

Secondary constituent: 7.7 attributes remaining harness costs.
