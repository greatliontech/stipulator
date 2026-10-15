# The structural dialect cannot state call absence, so "never constructs X" clauses fall back to weaker evidence

Lands: tooling-redesign 9.4

Activation condition: a structural requirement needs a call-absence proof
that the signature/import verbs demonstrably cannot carry. The census
preserves this condition; the example API below is a design sketch, not
an implementation commissioned by the mapping.

## Observed

A consuming repository carries a structural requirement of the shape "the
package registers its services on a server handed in by the embedder and never
constructs or owns the listener." One proposed assertion — the package has
no direct call to `net.Listen`, `net.ListenConfig.Listen` or `grpc.NewServer` —
is not expressible in the structural dialect and would prove only that
narrower property:

- `structural.NoImport` is transitive over the import graph, so forbidding
  `net` fails immediately through the package's gRPC dependency
  (`package -> google.golang.org/grpc -> net`). Transitivity is right for
  "no external module in custody" clauses, but it makes stdlib-package
  forbiddance unusable for any package with real dependencies.
- `FunctionSignature`/`Implements`/`ExportedData` state presence of a shape,
  not absence of a capability. The repository settled for
  `FunctionSignature` over the registration entry point (it takes
  `grpc.ServiceRegistrar`), which proves the handed-in surface exists but not
  that no listener-constructing path exists beside it.

## Resolution

A call-absence verb, for example:

```go
// The named packages' production code never calls any of the forbidden
// symbols (functions, methods, or type constructions), directly.
structural.NoCall(t, "example.com/mod/pkg", "net.Listen", "net.ListenConfig.Listen", "google.golang.org/grpc.NewServer")
```

The source boundaries are `stipulate/structural`'s import, signature and
shape assertions. A direct-call-only `NoCall` could prove only that narrow
property: it cannot establish absence of all indirect construction through
helpers, function values, interfaces or dependencies. The signature pin
does not supply the missing reachability argument either. At activation,
match the assertion's actual reach to the consumer's clause or explicitly
narrow the claim; unresolved indirect paths must not grant a universal
"never constructs" proof. A direct-import mode would still not distinguish
address-type use from listener construction. Retain positive and indirect
counterexamples before assigning analyzer-proof authority.
