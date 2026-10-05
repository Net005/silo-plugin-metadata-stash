package legacyprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWatchListRecoveryAddsWithoutRemovingUnresolvedMembers(t *testing.T) {
	writes := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/admin/collections":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []siloCollection{{ID: "watch", Title: "Stash | Watchlist", LibraryID: "16", Slug: "javbeacon-stash-preset-7-library-16", CollectionType: "manual"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/admin/collections/watch/items":
			_, _ = w.Write([]byte(`{"items":[{"media_item_id":"old","position":0}],"page":{"has_more":false}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v2/admin/collections/watch/items/new":
			writes = append(writes, "add")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	changed, complete, id, err := NewSiloClient(server.URL, "key").SyncExistingWatchList(context.Background(), "16", "Stash | Watchlist", []string{"new"}, false, 200)
	if err != nil || !complete || changed != 1 || id != "watch" || len(writes) != 1 {
		t.Fatalf("changed=%d complete=%v id=%q writes=%v err=%v", changed, complete, id, writes, err)
	}
}
