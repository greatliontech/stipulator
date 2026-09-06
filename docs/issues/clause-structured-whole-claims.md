# A clause-structured requirement still admits whole-requirement test claims

A binding may scope its claim to one payload clause, and coverage then
judges the policy per clause (REQ-evidence-clause-claim): a claim that
names no clause grants its evidence to every clause. That keeps every
existing corpus green — a whole claim covers what it covered before —
but it also means the false-green channel the clause mechanism
retires (a test witnessing one clause of a multi-clause requirement,
claimed for the whole) closes only where an author chooses to write
clause claims. A corpus that wants the discipline enforced would refuse
whole-requirement `tests`/`proves` claims on a requirement that
declares clauses — at write time, and as a hygiene problem for
existing records — leaving `implements` claims whole (an
implementation realizes the requirement, not a clause of it).

Whether that refusal should exist, and whether it is a manifest opt-in
per corpus or the default, is a discipline choice the consumers own:
it trades authoring friction (every clause-structured requirement
needs one claim per clause) for a closed channel.

Lands: user decision
