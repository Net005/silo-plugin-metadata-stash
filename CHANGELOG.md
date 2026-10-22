# Changelog

## [Unreleased]

- Browser userscript v1.3.3 gives **Listed in Collections** the same carousel controls as **More Like This**: hover-revealed left/right arrow buttons with edge gradients, a left fade once scrolled, arrow-key scrolling, and no visible scrollbar.

- Browser userscript v1.3.2 clears the hero while a backdrop preview plays: the context line, title, date/quality badges, overview, director/genre/studio lines and the cover fade out, leaving only the action toolbar. Layout is preserved so the toolbar does not move, and everything returns when the preview stops.

- Browser userscript v1.3.1 sizes **Listed in Collections** covers exactly like **More Like This** by mirroring the native poster-size wrapper inside each carousel slide.

- Browser userscript v1.3.0 adds a **Listed in Collections** row below **More Like This** on item pages. It lists every collection in the viewed library (plus personal collections) that contains the release, in Silo's collection order, with posters and links to each collection, and hides itself when there are none. Membership comes from the complete per-library collection lists (Silo's server-wide listing is a capped preview) and is cached for 10 minutes; an unreadable collection is retried instead of being cached as empty.

## [0.3.70] - 2026-10-18

- Add opt-in automatic recovery for scanned Stash files that lack a catalog association after yearless matching fails. Restrict recovery to enabled Stash movie/mixed libraries, exact source file identity and a verified release date.
- Preserve existing sidecars and media files, create only missing per-file NFO metadata, and request only a targeted file scan. Keep native matcher backoff and global confidence thresholds intact; report admitted work separately from verified completion.
- Expose the recovery opt-in in the connection settings and reject invalid values before applying any configuration changes. No companion or browser update is required.

## [0.3.69] - 2026-10-18

- Add bounded per-session diagnostics to the read-only playback backfill preview: exact session/item identities, completion time, matched Stash scene ID where available and a missing/present/unresolved/excluded/incomplete-history outcome. Keep file paths out of the report.
- Read each matched scene's history once per preview, sharing the fresh result across repeated sessions without caching it between previews. Preserve all no-write and incomplete-pagination safeguards.

## [0.3.68] - 2026-10-17

- Add a read-only completed-play backfill preview that works without JAVBeacon. Compare up to 100 newest unique completed movie sessions against exact Stash file identities and fresh history, report partial coverage and incomplete history, and never write activity.

## [0.3.67] - 2026-10-17

- Respect Silo's provider artwork storage setting in artwork caching and poster repair. Pause without losing cache progress when storage is disabled, and skip writes when the setting cannot be read.

## [0.3.66] - 2026-10-17

- Validate complete configuration requests before changing connections or starting background workers. Invalid settings preserve the working configuration.
- Include Stash release years in metadata search results to improve matching.

## Unreleased

- Browser userscript v1.2.1 records explicit O increments at the matched Stash scene's latest last-played timestamp, read fresh on each click. Scenes without playback history retain the current-time default.

## [0.3.65] - 2026-10-15

- Decode Silo's string-valued media file IDs in live session telemetry so active playback duration checkpoints reach Stash. Preserve file identity checks, resume positions and retry protections.

## [0.3.64] - 2026-10-13

- Offer the original Stash screenshot alongside the conformed poster in Silo's artwork picker, keeping the conformed poster first and the automatic metadata poster unchanged.
- Deduplicate repeated backdrop choices while retaining the same image as both a poster and a backdrop.
- Recognize absolute Unix library paths independently of the build host when performing exact scene lookup.
- Update to Silo plugin SDK v0.25.0 and its current gRPC dependency.

## [0.3.63] - 2026-10-11

- Measure active Silo playback from fresh advancing session telemetry every five seconds, scoped to the configured owner profile. Pauses, stale sessions, long gaps and large seeks cannot credit a video's full runtime.
- Save pending duration checkpoints in private host instance state and send additive Stash activity without overwriting resume positions. Reserve mutations before sending so uncertain responses cannot double count on retries or restarts. Known Stash read failures retain pending checkpoints.
- Use one duration writer for measured playback; keep resume and completed-play sync separate from duration. Add an admin-only measurement status endpoint. Requires SDK v0.24 private host state support; native Stash player tracking is unchanged.

## [0.3.62] - 2026-10-11

- Discard parked and pending Watchlist retries superseded by a newer completed action or queued Stash event. File availability recovery can no longer replay an older action over the user's newer choice.
- Retire an older parked export when accepting a newer explicit Stash Watchlist event, while retaining the newer event durably for retry.

## [0.3.61] - 2026-10-10

- Keep unavailable Watchlist additions queued without blocking newer Stash events or native Silo Watchlist recovery. Silo can retain file records after marking the files missing while refusing collection admission.
- Preserve newer additions ahead of an older queued Stash addition when its item becomes available again.

## [0.3.60] - 2026-10-10

- Apply explicit Stash Watchlist events to exact local files instead of scanning every library per click. Re-adds move to the first collection position, even when snapshot membership is unchanged. Journal incoming events before applying them; retry failures and ignore older delayed events.
- Stash companion v0.2.14 saves Watchlist tags atomically and enqueues a server-side sync job in the same GraphQL request. Native tag-only hooks also queue sync instead of blocking on Silo. Keep unrelated tags and release the button after the save and enqueue response.

## Unreleased

- Silo plugin v0.3.59 waits for inbound Watchlist reconciliation before acknowledging a Stash hook, preventing a quick remove/add from collapsing into an unchanged membership snapshot.

- Stash companion v0.2.13 restores the native sceneUpdate Watchlist mutation so Scene.Update.Post continues to synchronize Silo in realtime. Immediate UI feedback and the tag-only metadata fast path remain enabled.

- Stash companion v0.2.12 updates Watchlist buttons immediately, prevents duplicate saves, and restores the prior state on failure. Native scene saves preserve unrelated tags and trigger the realtime Silo synchronization hook. The button keeps its membership label while saving.
- Tag-only scene hooks retain collection synchronization and recommendation notification but skip unrelated enrichment, activity webhooks, and Silo metadata refresh.

## [0.3.58] - 2026-10-10

### Fixed

- Detect quick native Watchlist remove/add cycles using the newest entry’s addition timestamp, even when membership is unchanged between recovery polls.
- Read the complete paginated member order when Silo’s order endpoint returns only its first 200 entries, so large Watchlists can reorder safely.
- Order exported Watchlist members by their actual native addition date, newest first, with an ETag-protected reorder. Delayed recovery keeps the original timestamp and cannot overtake a newer addition. Preserve the relative order of collection-only members; both saved-filter sync and recovery retain this order.

### Improved

- Tampermonkey v1.1.4 sorts genre and studio filter links by release date descending (newest first).

- Tampermonkey v1.1.3 turns item genres into links and adds studio links, opening the current library with the exact genre or studio filter in a new tab. Existing hero studio labels also link to the same filter.

- Tampermonkey v1.1.2 shows the full release date (`YYYY-MM-DD`) in Silo’s existing item year badge, preserving its styling and retaining the year when a complete date is unavailable.

## [0.3.57] - 2026-10-10

### Fixed

- Restore six-hour saved-filter collection cover rotation while preserving unique member artwork across collections. Routine polls retain the current cover; scheduled rotations prefer another member and record the poster actually selected.
- Keep a collection with no unused member cover from stopping artwork and membership updates for all remaining collections.

### Improved

- Tampermonkey v1.1.1 restores an icon-only Watchlist action to the item toolbar, using Silo's Bookmark and BookmarkCheck icons, native toggle behavior, and existing button styling.
- Add Create Subtitle beside the overflow menu for missing or outdated subtitles. Match a configurable library-name allowlist (default JAV) and reuse the Stash companion's status checks and overwrite confirmation prompts.

## [0.3.56] - 2026-10-10

### Fixed

- Skip orphaned unmatched catalog rows, removed files, and rejected match requests so one stale item cannot prevent later libraries from matching. Authentication, throttling, and upstream errors remain visible.
