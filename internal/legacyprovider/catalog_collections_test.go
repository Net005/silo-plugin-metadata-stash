package legacyprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListJAVBeaconMovieLibrariesUsesEnabledProviderChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/libraries":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"id": "16", "type": "movies", "enabled": true},
				{"id": "19", "type": "movies", "enabled": true},
				{"id": "3", "type": "movies", "enabled": true},
				{"id": "20", "type": "movies", "enabled": false},
				{"id": "21", "type": "mixed", "enabled": true},
			}})
		case "/api/v2/libraries/16/providers", "/api/v2/libraries/19/providers", "/api/v2/libraries/3/providers", "/api/v2/libraries/21/providers":
			enabled := r.URL.Path != "/api/v2/libraries/3/providers"
			_ = json.NewEncoder(w).Encode(map[string]any{"levels": []map[string]any{{"content_level": "movie", "entries": []map[string]any{{"capability_id": "stash", "provider_slug": "stash", "enabled": enabled}}}}})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	libraries, err := NewSiloClient(server.URL, "key").ListJAVBeaconMovieLibraries(context.Background())
	if err != nil || len(libraries) != 3 || libraries[0].ID != "16" || libraries[1].ID != "19" || libraries[2].ID != "21" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
}

func TestSyncCollectionsPrunesOnlyOwnedZeroMatchCollections(t *testing.T) {
	owned := siloCollection{ID: "empty", Title: "Stash | Empty", Slug: "javbeacon-stash-preset-7-library-19", LibraryID: "19", Description: collectionOwner}
	other := siloCollection{ID: "user", Title: "My List", Slug: "my-list", LibraryID: "19", Description: "user"}
	deleted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v2/admin/collections" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []siloCollection{owned, other}})
		case r.URL.Path == "/api/v2/admin/collections/order" && r.Method == http.MethodGet:
			w.Header().Set("ETag", `"order"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"ordered_ids": []string{"empty", "user"}})
		case r.URL.Path == "/api/v2/admin/collections/empty" && r.Method == http.MethodDelete:
			deleted["empty"] = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	// The scope entry names an enabled library without creating a collection.
	specs := []CollectionSpec{{Kind: "library", LibraryID: "19"}}
	changed, complete, err := NewSiloClient(server.URL, "key").SyncCollectionsBatch(context.Background(), specs, 0, false, false, true)
	if err != nil || !complete || changed != 1 || !deleted["empty"] || deleted["user"] {
		t.Fatalf("changed=%d complete=%v deleted=%v err=%v", changed, complete, deleted, err)
	}
}

func TestSavedFilterReconcileLeavesWatchListCollectionUntouched(t *testing.T) {
	watchlist := siloCollection{ID: "watch", Title: "WatchList", Slug: "javbeacon-watchlist-library-19", LibraryID: "19", Description: collectionOwner}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/admin/collections" {
			writes++
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []siloCollection{watchlist}})
	}))
	defer server.Close()
	changed, complete, err := NewSiloClient(server.URL, "key").SyncCollectionsBatch(context.Background(), []CollectionSpec{{Kind: "library", LibraryID: "19"}}, 0, true, true, true)
	if err != nil || !complete || changed != 0 || writes != 0 {
		t.Fatalf("changed=%d complete=%v writes=%d err=%v", changed, complete, writes, err)
	}
}
