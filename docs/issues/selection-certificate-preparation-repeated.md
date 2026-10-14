# Selected-view preparation is repeated across certificate consumers

Lands: tooling-redesign 5.2e, when scoped discovery and certificate cold/warm costs are attributed.

Declaration inventory (`packageinventory.go`), native fingerprint capture
(`served.go`) and the complete selected-input certificate
(`sourcecertificate.go`) independently prepare a selection's environment,
module members, directory and flags. The endpoints need independent source
observations, but the rules resolving those configuration facts describe one
selected view. Repeated preparation adds cost and multiple places for a future
selection change to diverge.

Measure the repeated work under the same cold and warm workload before
changing sharing. A prepared, immutable endpoint-local selection context can
own the environment, member ownership and pass reader for those consumers.
Delete the parallel configuration derivations where the measured consolidation
is justified. Preserve per-owner graph failures and native source admission.
Opening and closing endpoints must retain separate file/graph observations;
sharing their digest memos would erase the closing check's authority.
