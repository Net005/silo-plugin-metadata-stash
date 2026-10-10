# Changelog

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
