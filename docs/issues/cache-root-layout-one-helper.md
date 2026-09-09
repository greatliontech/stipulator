# Three copies of the cache-root layout

`<user cache>/stipulator/<kind>/<sha256[:8] of a key>` is spelled three
times — the witness store (internal/witnesscache), the resolution store
(internal/resolutioncache), and the owned telemetry home
(internal/backends/golang/telemetry.go) — each calling os.UserCacheDir
and joining the same segments. One helper naming the layout would make
a moved cache root or a changed key width one edit; today it is three,
and the third copy was written without the first two in view.

Lands: with the next change set touching any of the three layouts.
