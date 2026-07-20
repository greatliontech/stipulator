# The structural dialect cannot state call absence, so "never constructs X" clauses fall back to weaker evidence

Lands: when a structural requirement next needs a call-absence proof and the
signature/import verbs demonstrably cannot carry it.

## Observed

A consuming repository carries a structural requirement of the shape "the
package registers its services on a server handed in by the embedder and never
constructs or owns the listener." The natural proof — the package never calls
`net.Listen`/`net.ListenConfig` (or never constructs `grpc.NewServer`) — is not
expressible in the structural dialect:

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

Direct-call absence over the package's own bodies (type-checked callee
resolution, no transitive chasing) is the useful floor and is cheap; whether
indirect calls through function values need a disposition can be stated in the
verb's contract (the reference consumer's clause is satisfied by direct-call
absence plus the existing signature pin). A non-transitive mode for `NoImport`
(direct imports only) would serve a subset of these clauses but does not
distinguish "imports net for an address type" from "constructs listeners", so
the call-level verb is the one that carries the clause.
