# Coverage-driven gap resolution forces out manual-condition gaps

Lands: when the gap lifecycle or prune residue semantics next change, or when `go/packages` symbol loading runs behind an owned boundary.

## Observed

A gap resolves the moment its requirement's coverage reaches the covered bucket — REQ-gap-lifecycle defines `resolved` as "its requirement is covered" — and the unified check reports every resolved gap as prune residue, which fails the verdict until the record is deleted. Resolution is therefore decided entirely by witness coverage; a gap has no way to say "the witnesses are green, but the requirement is still factually violated on a path no witness reaches."

That state is real. REQ-go-owned-processes requires every child process spawned for Go policy execution or package discovery to run inside an owned boundary whose descendant tree terminates with cancellation. Its witnesses are green, so the requirement reads covered — yet the `go/packages` symbol-loading launcher remains outside any owned boundary ([go-subprocess-tree-ownership](go-subprocess-tree-ownership.md)). The gap record that tracked this was force-deleted as prune residue, and re-filing it would immediately fail the check the same way. A witnessed-green requirement with a known unwitnessed violation is inexpressible in records today; the only surviving trace is this issue tracker, outside the record system the gate audits.

## Resolution

Give the gap lifecycle a resolution condition that coverage alone cannot discharge — a gap kind (or an explicit landing condition) that stays `open` while its requirement is covered, resolving only when its stated condition holds — and exclude such gaps from prune residue until then. Alternatively, if prune residue semantics change first, let residue reporting distinguish "covered with a standing condition" from "satisfied, dead record weight." Either shape must keep REQ-gap-resolved-pruned's property that a genuinely satisfied gap cannot linger.
