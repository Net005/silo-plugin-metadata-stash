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

Resources are requested only after a hover and reused while that item remains open. Navigation releases previews and object URLs. Nothing changes scene metadata, collections, play history or O counts. This is a visual preview, not a full playback session.

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
