# Stash.Metadata

Stash.Metadata is a dedicated Stash plugin that fills missing scene metadata from a **JAVBeacon release already linked to that exact Stash scene ID**. Stash owns scene metadata, covers, and play history. This plugin never writes play or O history and does not use Silo.

Existing metadata already stored in Silo is not reset by installing or updating this plugin. The migration task reads JAVBeacon’s already indexed releases through the linked Stash scene IDs and fills missing Stash fields without a full Silo refresh or source scrape. Silo-only manual edits that were never stored in JAVBeacon cannot be inferred from that cache; export those separately before removing Silo if you need them in Stash.

It also includes the existing JAVBeacon Stash scene link, realtime change webhook, subtitles controls, Watchlist button, and cover/scrubber previews in one plugin.

## What it changes

- Fills empty scene title, code, details, director, date, and source URL. Existing values are preserved.
- Reuses studios, performers, and tags by exact name. Creating missing entities is optional and off by default. Existing scene associations are preserved.
- Can fetch a JAVBeacon poster through its resize/crop route **only if Stash reports no screenshot** and `cover_mode` is `missing`. Cover updates are off by default and are performed only by the explicit enrichment task, never by scene hooks.
- On scene create/update, sends the existing realtime webhook when configured and independently attempts fill-only enrichment. Scenes not yet linked in JAVBeacon are skipped; rerun the backfill after the link is established.
- Scans 200 scenes per manual backfill by default. The log includes `next_page` and `next_index` to resume exactly at the next scene; pass these as task arguments for subsequent runs. Set `max_scenes_per_run` to `0` for a full scan.

## Install

1. Download the latest release ZIP and extract its `stash-metadata` directory under Stash's `plugins` directory, so `stash-metadata/stash-metadata.yml` exists. Reload Stash plugins.
2. Set **JAVBeacon URL** and **JAVBeacon API key** under Settings → Plugins → Stash.Metadata. The key stays in the server-side Python plugin; the browser UI uses Stash plugin tasks.
3. Set the optional **JAVBeacon webhook secret** for realtime scene-change sync. Copy the other settings from the older JAVBeacon Stash plugin if you used subtitles, Watchlist, or player enhancements.
4. Run **Preview metadata enrichment** and review the Stash plugin log. Then run **Enrich missing scene metadata**. Automatic create/update enrichment also uses the same fill-only rules.
5. After checking the replacement in Stash, remove the older JAVBeacon Stash plugin and Silo JAVBeacon plugin from their respective plugin directories to avoid duplicate hooks and buttons.

JAVBeacon must be updated to a version providing the API-key-protected `GET /api/v1/integrations/stash/enrichment/{sceneId}` and `/covers/{id}/stash-poster` endpoints before enrichment can work. The realtime webhook remains a separate secret-based endpoint.

## Development

```sh
python3 -m unittest discover -q
node --test test_javbeacon_subtitles.js test_javbeacon_scrubber.js
```
