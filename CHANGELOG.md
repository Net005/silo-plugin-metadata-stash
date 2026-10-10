# Changelog

## Unreleased

- Browser userscript v1.2.1 records explicit O increments at the matched Stash scene's latest last-played timestamp, read fresh on each click. Scenes without playback history retain the current-time default.

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
