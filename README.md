# Stash.Metadata

A **Go Silo plugin** that uses StashApp as its metadata, artwork, playback and Watchlist source. The Silo capabilities are `metadata_provider.v1`, `image_resolver.v1`, and `watch_sync_provider.v1`. The StashApp companion lives in [`contrib/stashapp`](contrib/stashapp) and enriches Stash from JAVBeacon's existing index; JAVBeacon is not in the Silo plugin's runtime path.

## Install

1. Download the Silo plugin binary for your architecture from the latest release, then install it in Silo. Configure the StashApp URL, Stash API key and, if used, Watchlist tag ID. Select **Stash.Metadata** as the movie library metadata provider and connect its Stash watch provider.
2. Extract the release's `stash-metadata-companion` ZIP into Stash's plugins directory. Reload Stash plugins, then configure its JAVBeacon URL and API key. The companion's existing subtitles, scene link, Watchlist button and player preview features are included.
3. After verifying the new provider, remove the old Silo JAVBeacon plugin and old Stash JAVBeacon realtime plugin to avoid duplicate hooks and controls. Existing Silo collections remain stored, but JAVBeacon saved-filter collections are no longer updated by this replacement.

The Go provider matches Stash scenes by an existing `stash` provider ID or an exact normalized scene code, title, or file stem. Ambiguous searches are not auto-selected. Metadata and screenshot URLs come directly from Stash. Completed Silo plays are added to Stash only when their event timestamp is not already in Stash play history; retrying the same event does not add a duplicate. Resume checkpoints are sent without inflating play duration. Watchlist events update the configured Stash tag while retaining other tags. Stash watched state is imported to Silo in pages. A bounded exact-match pass runs on startup and every 15 minutes when the Silo admin URL and key are configured; it is also exposed as a scheduled task. The companion can queue a targeted Silo refresh after a Stash scene edit when the same Silo connection is configured and it finds one exact catalog item.

## Existing Silo metadata

Installing this plugin does not delete Silo's stored metadata. To move Silo's cached fields into Stash **without a metadata refresh**, use the companion's **Preview existing Silo metadata migration** task, then **Import existing Silo metadata**. Configure the Silo URL, API key and library ID temporarily. The task reads matched catalog records, identifies a Stash scene by the exact stored scene ID or a unique exact code/title/file-stem match, and fills only empty Stash fields. Ambiguous rows are skipped. It does not change plays, O counts or existing Stash values. The bounded task reports a cursor and index for resumption.

The companion's separate **Migrate cached metadata to Stash** task fills remaining empty fields from JAVBeacon's already indexed releases by exact Stash scene ID; it does not scrape or refresh Silo. JAVBeacon v1.0.280 or newer is required for that task.

## Development

```sh
go test ./...
go vet ./...
python3 -m unittest discover -s contrib/stashapp -q
node --test contrib/stashapp/test_stash_subtitles.js contrib/stashapp/test_stash_scrubber.js
```
