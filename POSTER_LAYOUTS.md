# Local non-JAV poster layouts

The Go plugin renders these posters itself. JAVBeacon and Silo need no server
changes; no OpenAI requests or Python runtime are involved. Pigo's small MIT
licensed cascade is embedded with its licence in `internal/poster`.

In **StashApp Connection**, enable **non-JAV poster layout repair** and select
`contain` (recommended) or `face`. The worker runs hourly with a five-minute
budget. **Repair non-JAV poster layouts** admits a background run; final counts
and failures are in the plugin log. Configure the existing recommendation owner
profile because catalog reads require a profile. The setting is opt-in and is
not enabled automatically by installing an update.

Only enabled movie/mixed libraries with the Stash Metadata provider are scanned.
Each item must have a unique exact Stash file-path match. JAV-like scene codes,
missing sources and manually locked artwork are skipped. No title-only match
can replace a poster. Only the poster changes: backdrops, play/O counts and
watch state are untouched.

`contain` keeps the complete original on a dark blurred 600×900 portrait card,
with existing title and studio/date metadata in the footer. `face` optionally
centres a crop on one confident face with a margin. Multiple or uncertain faces
fall back to `contain`. Face detection does not recognise printed titles; use
`contain` for title cards. Detection uses at most 400px-wide input while rendering
uses the full-resolution Stash source. Input bytes and decoded dimensions are
bounded. Upscaling cannot recreate missing image detail.

The plugin exposes one unpredictable, temporary loopback image URL while Silo's
acting-admin image API copies the JPEG into its configured local/S3 storage.
The listener closes immediately afterward. Browsers use Silo's stored artwork,
not this URL. Durable hidden state records keep source/layout fingerprints,
source content hashes, stored paths and per-library cursors. Unchanged sources
are skipped; their content is rechecked weekly. The worker respects existing
image locks and restores the lock added by the image API.

The renderer was verified with nonexplicit preview artwork and synthetic images.
No bulk artwork repair is run as part of installation.
