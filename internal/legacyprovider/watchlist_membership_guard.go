package legacyprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The local/remote baseline is advanced only for membership changes this inbound
// reconciler actually performs. A later full snapshot must never acknowledge a
// user's concurrent edit and thereby erase its outbound intent.
func (c *SiloClient) guardedCollectionMembership(ctx context.Context, collection siloCollection, media string, desired, wasMember bool, position int) error {
	path := "/api/v2/admin/collections/" + url.PathEscape(collection.ID)
	if len(collection.SourceConfig["stash_watchlist_outbox"]) == 0 {
		method := http.MethodDelete
		var payload any
		if desired {
			method = http.MethodPut
			payload = map[string]int{"position": position}
		}
		return c.collectionRequest(ctx, method, path+"/items/"+url.PathEscape(media), payload, nil)
	}
	var current siloCollection
	if err := c.collectionRequest(ctx, http.MethodGet, path, nil, &current); err != nil {
		return err
	}
	// The caller snapshot can precede a local edit; read membership again.
	members, err := c.collectionMembers(ctx, collection.ID)
	if err != nil {
		return err
	}
	_, wasMember = members[media]
	raw := current.SourceConfig["stash_watchlist_outbox"]
	var journal map[string]json.RawMessage
	baseline := map[string]bool{}
	pending := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if json.Unmarshal(raw, &journal) != nil || json.Unmarshal(journal["baseline"], &baseline) != nil || json.Unmarshal(journal["pending"], &pending) != nil {
			return fmt.Errorf("invalid Watchlist journal; membership preserved")
		}
		if len(pending[media]) > 0 {
			return fmt.Errorf("pending local Watchlist action; membership preserved")
		}
		if wasMember != baseline[media] && desired != wasMember {
			return fmt.Errorf("local Watchlist edit awaiting export; membership preserved")
		}
	}
	method := http.MethodDelete
	var payload any
	if desired {
		method = http.MethodPut
		payload = map[string]int{"position": position}
	}
	if err := c.collectionRequest(ctx, method, path+"/items/"+url.PathEscape(media), payload, nil); err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	tag, err := c.collectionRequestETag(ctx, http.MethodGet, path, nil, &current, "")
	if err != nil {
		return err
	}
	if json.Unmarshal(current.SourceConfig["stash_watchlist_outbox"], &journal) != nil {
		return fmt.Errorf("Watchlist journal unavailable")
	}
	if json.Unmarshal(journal["baseline"], &baseline) != nil {
		return fmt.Errorf("Watchlist baseline unavailable")
	}
	if desired {
		baseline[media] = true
	} else {
		delete(baseline, media)
	}
	journal["baseline"], _ = json.Marshal(baseline)
	current.SourceConfig["stash_watchlist_outbox"], _ = json.Marshal(journal)
	_, err = c.collectionRequestETag(ctx, http.MethodPatch, path, map[string]any{"source_config": current.SourceConfig}, nil, tag)
	return err
}
