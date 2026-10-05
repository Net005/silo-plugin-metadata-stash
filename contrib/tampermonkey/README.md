# Silo Stash Backdrop Hover

Install `silo-backdrop-hover.user.js` using Tampermonkey → Create a new script → replace the editor contents → Save.

Open a Silo movie, then use the Tampermonkey menu **Backdrop hover: settings / delay / API keys**.

- Set **Hover delay** in milliseconds. Default: **400 ms**, matching the Stash companion.
- Enter your **Stash API key**. It is kept in Tampermonkey storage, not embedded in the script.
- Enter your **Silo admin API key** if the current Silo session cannot read the item's file paths. The script first tries the existing browser access token. It never refreshes or modifies your login session.
- **Thumbnail interval** defaults to **700 ms**, matching the companion fallback.

Move the mouse over the backdrop outside the poster, title/information and controls. After the delay, a muted looping Stash preview replaces the backdrop. If video is unavailable or cannot play, the script cycles through Stash's VTT thumbnail frames. Moving away restores the original backdrop. Silo's gradient treatment, text and buttons remain in place. The preview is contained so it does not crop faces or show adjacent sprite frames.

The script finds a scene by a unique exact file-path match. When your Stash and Silo paths differ, use **Backdrop hover: assign Stash scene for this item** and paste the numeric Stash scene ID or scene URL. This assignment is stored per Silo item. Blank clears it.

Resources are requested only after a hover and reused while that item remains open. Navigation releases previews and object URLs. Nothing changes scene metadata, collections, play history or O counts. This is a visual preview, not a full playback session.

If neither a preview nor thumbnails exist, generate those assets in Stash first. Stash must be reachable from your browser. A failed lookup displays a short explanation and keeps the original backdrop.

Verified: JavaScript syntax, VTT sprite coordinates, exact matching and ambiguity rejection. Live browser verification was unavailable because the browser security check rejected access; selectors were checked against the local Silo `DetailHero` frontend source.
