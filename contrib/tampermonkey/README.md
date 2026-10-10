# Silo Stash Backdrop Hover

Install `silo-backdrop-hover.user.js` using Tampermonkey → Create a new script → replace the editor contents → Save.

Before saving, replace the example `@match` line with your Silo URL pattern **in your local Tampermonkey editor only**. Private server addresses are configured locally and must not be committed to this repository. The `@connect *` declaration supports your chosen Stash host; requests are restricted in code to the configured Stash origin. Tampermonkey may ask permission for that host.

Open a Silo movie, then use the Tampermonkey menu **Backdrop hover: settings / delay / API keys**.

- Set **Hover delay** in milliseconds. Default: **400 ms**, matching the Stash companion.
- Enter your **Stash server URL** (origin only, without a path).
- Enter your **Stash API key**. It is kept in Tampermonkey storage, not embedded in the script.
- Enter your **Silo admin API key** if the current Silo session cannot read the item's file paths. The script first tries the existing browser access token. If the browser access token expires, it uses Silo’s normal refresh endpoint once and saves the renewed session tokens. A configured API key is never replaced or refreshed.
- **Thumbnail interval** defaults to **700 ms**, matching the companion fallback.

Move the mouse over the backdrop outside the poster, title/information and controls. After the delay, a muted looping Stash preview replaces the backdrop. If video is unavailable or cannot play, the script cycles through Stash's VTT thumbnail frames. Moving away restores the original backdrop. Silo's gradient treatment, text and buttons remain in place. The preview is contained so it does not crop faces or show adjacent sprite frames.

The script finds a scene by a unique exact file-path match. When your Stash and Silo paths differ, use **Backdrop hover: assign Stash scene for this item** and paste the numeric Stash scene ID or scene URL. This assignment is stored per Silo item. Blank clears it.

Resources are requested only after a hover and reused while that item remains open. Navigation releases previews and object URLs. Hover previews do not change scene metadata, collections, play history or O counts. Toolbar actions are separate explicit clicks: Watchlist updates membership, the Flame button adds one to the Stash O count, and Create Subtitle can queue generation after the checks described below.

If neither a preview nor thumbnails exist, generate those assets in Stash first. Stash must be reachable from your browser. A failed lookup displays a short explanation and keeps the original backdrop.

Verified: JavaScript syntax, VTT sprite coordinates, exact matching and ambiguity rejection. Live browser verification was unavailable because the browser security check rejected access; selectors were checked against the local Silo `DetailHero` frontend source.

Version 1.0.1 fixes expired-session HTTP 401 lookups, paginates file versions, and
uses `libraryId` from the item URL to exclude files in other libraries. An expired
signed S3 avatar URL is not a Stash scene ID and is unrelated to preview lookup.
After updating the script, reload Silo. If your login cannot refresh, sign in again
or configure a valid Silo admin API key; non-admin sessions still require an admin
key or a manual scene assignment.

Version 1.0.2 hides the story/overview (including its expand/translate controls)
only after a video or thumbnail preview is visible. It restores the overview on
mouse leave, failed playback, tab hiding, navigation or disabling the script.
The space is retained to avoid layout jumps; title, metadata and playback controls
remain visible.

Version 1.0.3 removes deployment-specific domains. Enter the Stash URL locally after updating; existing API keys and hover preferences are retained.

## Toolbar actions (1.1.0)

The settings dialog now includes **Subtitle library names**. It defaults to `JAV`;
enter exact library names separated by commas, semicolons, or newlines. A blank
allowlist disables Create Subtitle. Library IDs are resolved to names using
Silo's library API, and an item URL's `libraryId` scopes the file lookup.

**Create Subtitle** appears immediately left of More actions only for an
allowed library with missing subtitles or confirmed old/outdated subtitles.
Current subtitles and subtitles whose freshness is unknown keep the action
hidden. The script resolves a unique Stash scene through the same exact-path
lookup/manual assignment as the backdrop preview, and uses the installed
`stash-silo-companion` plugin's `subtitle_status` and `subtitles` operations.
It checks again when clicked. Replacement confirmations use the companion's
same JavaScript approval/cancellation prompts, including its backend names.
Cancellation sends no generation request. A status error hides the action
rather than assuming an existing subtitle is outdated.

Watchlist is an icon-only native glass button in the main toolbar. It copies
Silo's own current Plus/Check menu icon, invokes the native Watchlist handler,
and hides the duplicate Watchlist entry in More actions. This keeps Silo's
profile state, cache invalidation, and feedback. Both toolbar additions work
independently of whether backdrop hover is enabled. Their button classes come
from Silo's current More action, with native text-button sizing for subtitles;
there are no custom button colors, borders, backgrounds, or theme overrides.

Update the installed userscript from this file, retaining your local `@match`
line, then reload Silo. Existing keys and hover settings are preserved. The
new allowlist is editable from **Backdrop hover: settings / delay / API keys**.
Syntax and six request/eligibility/confirmation tests pass. The toolbar was
checked against Silo's current `ActionBar` source; live visual verification
remains unavailable because the browser policy check cannot grant access.

The toolbar Watchlist toggle uses Silo’s Lucide Bookmark (not added) and BookmarkCheck (added) icons. The existing native button styling and overflow action are preserved.

Version 1.1.2 displays a complete release date as `YYYY-MM-DD` in the existing item year badge. Missing, partial or invalid dates leave the year unchanged.

Version 1.1.3 makes item genres and studios clickable. Links open a new tab with Silo’s exact-match filter in the item’s library, using native query parameters and existing theme classes.

Version 1.1.5 adds an O-count action beside Watchlist using Silo’s Lucide Flame icon and native glass button styling. A positive count appears beside the icon; zero or an unset count shows the icon alone. Each click records one O in Stash and displays the confirmed total. Requests are guarded against double clicks and are never automatically retried. If a request cannot be confirmed, refresh the page before trying again. The action appears only for a uniquely matched or manually assigned Stash scene whose count can be read.
