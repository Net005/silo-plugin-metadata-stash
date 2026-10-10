# Changelog

## [0.3.58] - 2026-10-10

### Fixed

- Detect quick native Watchlist remove/add cycles using the newest entry’s addition timestamp, even when membership is unchanged between recovery polls.
- Move exported Watchlist additions to first position with an ETag-protected reorder, preserving the order of other members. Saved-filter synchronization retains this newest-first order.

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
