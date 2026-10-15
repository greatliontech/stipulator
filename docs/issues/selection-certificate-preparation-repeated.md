# Selected-view preparation is repeated across certificate consumers

Lands: tooling-redesign 5.2e

Declaration inventory (`packageinventory.go`), native fingerprint capture
(`served.go`) and the complete selected-input certificate
(`sourcecertificate.go`) independently prepare a selection's environment,
module members, directory and flags. The endpoints need independent source
observations, but the rules resolving those configuration facts describe one
selected view. Repeated preparation adds cost and multiple places for a future
selection change to diverge.

Complete selections, module mode, physical/dotted declaration identities,
earlier-declaration absence and cross-view certificates are already implemented
by 5.2a/b. Preserve those dependencies and format 5's strict certificate/native
admission; the remaining issue is repeated preparation cost, not a reopened
certificate-completeness defect.

Measure the repeated work under the same cold and warm workload before
changing sharing. A prepared, immutable endpoint-local selection context can
own the environment, member ownership and pass reader for those consumers.
Delete the parallel configuration derivations where the measured consolidation
is justified. Preserve per-owner graph failures and native source admission.
Opening and closing endpoints must retain separate file/graph observations;
sharing their digest memos would erase the closing check's authority.

Secondary constituent: 7.8 carries any broader sharing justified by the
endpoint-local measurements under complete generation identities.
