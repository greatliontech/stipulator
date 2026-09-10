# Is the resolver protocol's JSON-lines exchange within REQ-core-proto-io?

REQ-core-proto-io says every machine-consumed input and output of
stipulator is expressible as protobuf messages. The exchange between
an invocation and its self-executed resolver child is JSON lines over
the child's standard streams (resolverRequest/resolverResponse), one
binary at both ends, nothing persisted. Chunk 222 chartered a carve-out
and its review refuted the premise: the structs are trivially
expressible, so the sentence would have removed a constraint rather
than stated a fact. Two readings remain: the clause is met as it stands
(nothing to do), or the protocol should be a proto message on the wire
(a code change, with the handshake identity 224 added riding it).

Lands: user decision.
