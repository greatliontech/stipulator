# `pin --req` says "pins current" for unchanged text, leaving the re-consent in doubt

Consumer report (bldc, 2026-08-29): a batch `pin --req A,B,C` where
only A's text changed prints "B: pins current" — read in the moment
as "did the re-consent happen, or was it skipped?". "B: text
unchanged; nothing to re-consent" removes the doubt. (pin.go now
distinguishes "clause pins current; shape moved"; the plain arm still
reads as quiescence.)

Lands: user decision (consumer report from bldc, 2026-09-02 — the tool owner sequences).
