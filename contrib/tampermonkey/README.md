# Silo browser enhancements

[← Stash Metadata overview](../../README.md)

A Tampermonkey userscript that adds Stash previews and compact controls to Silo, using Silo's existing styling.

## Features

| Where | Enhancement |
| --- | --- |
| **Movie backdrop** | Muted looping Stash preview on hover, with VTT thumbnail fallback. Leaving restores the original artwork and overview. |
| **Watchlist** | Bookmark toggle in the main toolbar, using Silo's native Watchlist action and profile state. |
| **O count** | Water-droplet button shows the current positive scene count. Each click records **one O** in Stash and displays the confirmed total. |
| **Person page** | Water-droplet O-count badge beside age for a linked Stash performer with a positive count. |
| **Subtitles** | Compact **+ Sub** action for allowed libraries with missing or confirmed outdated subtitles. |
| **Stash link** | Northeast arrow opens the exact matched scene in a new tab. Hover text: **Open in Stash**. |
| **Rating** | One star and the selected rating at rest. Hover or keyboard focus reveals all five stars without widening the toolbar row. |
| **Metadata** | Full release dates as `YYYY-MM-DD`; clickable genres and studios open exact library filters in a new tab. |

## Install or update

1. Install Tampermonkey in your browser.
2. Open [silo-backdrop-hover.user.js](silo-backdrop-hover.user.js), copy its contents into **Tampermonkey → Create a new script**, and save.
3. Replace the example `@match` line with your Silo URL pattern **in your local editor**. Keep private server addresses out of this repository.
4. Open Silo and use **Tampermonkey → Backdrop hover: settings / delay / API keys** to configure the settings below.
5. Reload Silo after installing or updating. When replacing the script, retain your local `@match` line; settings stored by Tampermonkey are preserved.

Use **version 1.2.0+** for the person-page O-count badge.

## Settings

| Setting | What to enter |
| --- | --- |
| **Stash server URL** | Your Stash origin, without a path. It must be reachable from the browser. |
| **Stash API key** | Your Stash key; stored in Tampermonkey rather than embedded in the script. |
| **Silo admin API key** | Optional when your current Silo session can read item file paths. Use it if file lookup is unavailable to your session. |
| **Hover delay** | Delay before starting a preview. Default: **400 ms**. |
| **Thumbnail interval** | Time between fallback frames. Default: **700 ms**. |
| **Subtitle library names** | Exact names separated by commas, semicolons or newlines. Default: `JAV`. Blank disables **+ Sub**. |

The script first uses your existing Silo browser session. If its access token expires, it tries Silo's normal refresh endpoint once and saves the renewed tokens. A configured API key is never refreshed or replaced. Tampermonkey may ask permission to access your Stash host; outgoing Stash requests are restricted to the configured origin.

## Preview a scene

Hover the backdrop outside the poster, title, metadata and controls. After the configured delay, a muted Stash preview plays. If video is unavailable, the script cycles through Stash's VTT thumbnail frames.

The overview hides only while a preview is visible, retaining its space to avoid layout jumps. Leaving the backdrop, hiding the tab, navigating or disabling previews restores it. Preview resources load on demand and are reused while the item stays open.

Matching uses a **unique exact file path**. The URL's `libraryId`, when present, scopes file lookup to that library. If Stash and Silo paths differ, use **Backdrop hover: assign Stash scene for this item** and paste the numeric scene ID or scene URL. A blank assignment clears it.

## Record an O

Click the droplet beside Watchlist to add **one** to the matched scene's Stash O count. Zero or an unset count shows the icon alone; a positive count appears beside it. The button appears only when a unique match or manual assignment resolves and its count can be read.

The script blocks duplicate clicks while the request is pending and does not automatically retry O mutations. If the result cannot be confirmed, refresh before trying again.

On person pages, the badge reads the linked **Stash performer's** O count using its exact provider identity or legacy integration link. It hides zero, unavailable counts and people without a Stash identity. The person badge is informational.

## Request subtitles

First configure **JAVBeacon-Subs base URL** and **API token** in the installed **Stash.Silo Companion** plugin. Then add the desired Silo libraries to the userscript's **Subtitle library names** setting.

**+ Sub** appears only when the companion reports missing or confirmed outdated subtitles for the exact scene in an allowed library. Current subtitles, unknown freshness and status errors keep it hidden. Library IDs are resolved to names through Silo's library API.

Clicking checks the status again before submitting. Existing subtitles require the companion's replacement confirmation; cancelling sends no generation request. Requests use the companion's `subtitle_status` and `subtitles` operations.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| **Nothing appears** | Ensure the local `@match` pattern covers your Silo URL, enable the script and reload the page. |
| **No preview or thumbnails** | Generate preview assets in Stash and confirm your browser can reach Stash. |
| **Scene lookup fails** | Check exact file paths, selected library and API access, or assign the scene manually. Ambiguous matches are rejected. |
| **HTTP 401** | Sign in to Silo again if session renewal fails, or configure a valid admin API key. |
| **No person O badge** | The performer needs a Stash identity and a positive readable count. A name match alone is not used. |
| **No + Sub button** | Check the library allowlist, companion installation, subtitle service settings and subtitle status. |

Toolbar actions work independently of whether backdrop previews are enabled. Previews never alter scene metadata, Watchlist membership, playback history or O counts.
