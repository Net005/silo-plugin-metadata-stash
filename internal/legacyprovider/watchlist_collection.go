package legacyprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SyncExistingWatchList reconciles only membership of a uniquely identified
// existing manual collection. It never creates, renames or deletes collections.
func (c *SiloClient) SyncExistingWatchList(ctx context.Context, libraryID, collectionTitle string, desired []string, allowRemovals bool, maxChanges int, artwork ...[]CollectionArtwork) (int, bool, string, error) {
	collections, err := c.collections(ctx, libraryID)
	if err != nil {
		return 0, false, "", err
	}
	matches := []siloCollection{}
	for _, item := range collections {
		if item.LibraryID == libraryID && strings.EqualFold(item.Title, collectionTitle) && strings.HasPrefix(item.Slug, "javbeacon-stash-preset-") {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return 0, true, "", nil
	}
	if len(matches) != 1 {
		return 0, false, "", fmt.Errorf("silo: ambiguous WatchList collection in library %s", libraryID)
	}
	collection := matches[0]
	if collection.CollectionType != "" && collection.CollectionType != "manual" {
		return 0, false, collection.ID, fmt.Errorf("silo: WatchList collection is not manual")
	}
	members, err := c.collectionMembers(ctx, collection.ID)
	if err != nil {
		return 0, false, collection.ID, err
	}
	ordered := []string{}
	want := map[string]bool{}
	for _, id := range desired {
		if id != "" && !want[id] {
			want[id] = true
			ordered = append(ordered, id)
		}
	}
	changed := 0
	prefix := "/api/v2/admin/collections/" + url.PathEscape(collection.ID) + "/items/"
	for position, id := range ordered {
		if _, exists := members[id]; exists {
			continue
		}
		if maxChanges > 0 && changed >= maxChanges {
			return changed, false, collection.ID, nil
		}
		if err := c.collectionRequest(ctx, http.MethodPut, prefix+url.PathEscape(id), map[string]int{"position": position}, nil); err != nil {
			return changed, false, collection.ID, err
		}
		changed++
	}
	if allowRemovals {
		for id := range members {
			if want[id] {
				continue
			}
			if maxChanges > 0 && changed >= maxChanges {
				return changed, false, collection.ID, nil
			}
			if err := c.collectionRequest(ctx, http.MethodDelete, prefix+url.PathEscape(id), nil, nil); err != nil {
				return changed, false, collection.ID, err
			}
			changed++
		}
	}
	needsOrder := len(ordered) > 0 && allowRemovals
	if needsOrder && len(members) == len(ordered) {
		needsOrder = false
		for index, id := range ordered {
			if members[id] != index {
				needsOrder = true
				break
			}
		}
	}
	if needsOrder {
		if maxChanges > 0 && changed >= maxChanges {
			return changed, false, collection.ID, nil
		}
		path := "/api/v2/admin/collections/" + url.PathEscape(collection.ID) + "/items/order"
		var current json.RawMessage
		etag, err := c.collectionRequestETag(ctx, http.MethodGet, path, nil, &current, "")
		if err != nil {
			return changed, false, collection.ID, err
		}
		if etag == "" {
			return changed, false, collection.ID, fmt.Errorf("silo: WatchList order ETag missing")
		}
		if _, err := c.collectionRequestETag(ctx, http.MethodPut, path, map[string]any{"ordered_ids": ordered}, nil, etag); err != nil {
			return changed, false, collection.ID, err
		}
		changed++
	}
	if len(artwork) > 0 && (strings.HasPrefix(collection.Slug, "javbeacon-stash-preset-") || strings.HasPrefix(collection.Slug, "javbeacon-watchlist-")) {
		if maxChanges > 0 && changed >= maxChanges {
			return changed, false, collection.ID, nil
		}
		spec := CollectionSpec{Kind: "watchlist", LibraryID: libraryID, MediaIDs: ordered, Artwork: artwork[0]}
		artChanged, err := c.syncCollectionArtwork(ctx, collection, spec, time.Now())
		if err != nil {
			return changed, false, collection.ID, err
		}
		if artChanged {
			changed++
		}
	}
	return changed, true, collection.ID, nil
}
