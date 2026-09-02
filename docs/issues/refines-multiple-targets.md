# `refines` admits one target; a requirement refining two broader ones loses the second edge

Consumer report (bldc, 2026-08-30): `REQ-cli-write-path` refines
`REQ-history-single-write-path` and is also the standalone face's
instance of `REQ-core-one-source-form`'s store clause; the format
admits one `refines` target, so the second relationship is simply
unstated. bldc tracks its side as docs/issues/refines-single-target.md
(Lands: when the spec format admits more than one refines target).
Ask: admit a list of refines targets (the canonical form ordering
them), with the impact and coverage surfaces reading every edge.

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
