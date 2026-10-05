# JAVBeacon Silo plugin feature parity

Stash Metadata uses StashApp for primary scene metadata. JAVBeacon remains an optional source for features tied to its release index and playback engine. Full parity for those features requires JAVBeacon v1.0.285 or newer, its URL and API key, and a Silo admin URL and API key where noted.

| Old Silo plugin behavior | Stash Metadata replacement |
| --- | --- |
| JAVBeacon release metadata, Stash gap filling | Direct Stash metadata; the Stash companion fills missing fields from indexed JAVBeacon releases. Backend-only releases retain a metadata and artwork fallback by exact old release ID. |
| Resized portrait cover, original cover and screenshot backdrops | Exact linked release uses JAVBeacon's 1000×1500 conformed cover; original cover, cached screenshots and Stash scene screenshot remain backdrop candidates. |
| Performer photos, birth dates, biography and homepage | Stash performer IDs/photos plus JAVBeacon performer detail and bounded Silo person patches. |
| Playback checkpoints and completion | Configured JAVBeacon playback engine receives stable session events, including release-only items. Without that connection, direct Stash playback sync retains play and resume support. Neither path infers an O from a completed play. |
| Completed Silo play backfill | `play-backfill` task uses finalized sessions and JAVBeacon's duplicate-safe backfill endpoint. Partial attempts and O counts are excluded. |
| Stash watched state in Silo | Independent 30-second worker and `watched-sync` task mark exact local unplayed items watched. |
| JAVBeacon and Stash saved-filter collections | `collection-sync` preserves old Silo collection slugs/IDs, source order and six-hour cover rotation. Exact release path, linked scene ID and artwork identity precede unique code/title fallbacks. |
| Stash WatchList collection | Stash companion updates the selected prefixed Watchlist saved-filter collections in realtime; a one-minute worker and `watchlist-collection-sync` task recover missed hooks directly from the selected Stash saved filter. Silo's personal Watchlist is intentionally excluded. |
| Unmatched and partial match repair | One-minute exact filename matcher and `repair-matched` task reapply only verified linked Stash scenes missing cast or metadata. |
| Incremental metadata refresh | Fifteen-second worker and `metadata-refresh` task submit bounded Silo refresh jobs, verify completion, then acknowledge the JAVBeacon change cursor. |
| Concurrent metadata/artwork calls | Concurrent Stash scene requests are coalesced; play-history checks always fetch fresh state. The JAVBeacon compatibility client retains its bounded metadata cache. |

The old plugin never supplied a Silo web O-count button or incremented O automatically from a completed play; its UI note described a button as unimplemented. O counts change only through an explicit O action in JAVBeacon or Stash. The `javbeacon://` image scheme remains resolvable for existing Silo artwork references.

## Safety boundaries

- Exact Stash scene IDs are used wherever available. Ambiguous titles, scene IDs or collection selections are skipped.
- Scheduled collection reconciliation never creates or deletes the existing WatchList collection. It does not remove members if a local source entry cannot be resolved exactly.
- Completed-play backfill is routed through the backend's idempotent endpoint. The live services must be upgraded before these paths can be verified end to end.
