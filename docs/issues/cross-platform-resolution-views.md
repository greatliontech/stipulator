# Conditional off-host resolution-only views

Lands: tooling-redesign 9.4

Activation condition: a consumer policy declares an off-host GOOS/GOARCH
selection and requires resolution-only hygiene for its symbols. Mapping the
issue to the final census preserves that trigger; it does not commission a
new view now.

The Go policy normalization and selected-view paths in
`internal/backends/golang` refuse unsupported off-host execution by name.
That remains sound: typed resolution on the host cannot confer witness
evidence for an unexecuted foreign platform. Complete build selections,
module mode and cross-view certificates are already implemented; this is a
different conditional capability, not a reopening of those repairs.

The earlier proposal was a resolution-only view, allowing foreign-platform
bindings to resolve while granting no witness outcome. At activation, define
that authority and its selection/certificate identity, keep the execution
refusal, and pin a foreign-only declaration against a host control. The
original census found no such consumer among the four tooling corpora;
recheck that observation at 9.4 rather than assuming it permanently true.
