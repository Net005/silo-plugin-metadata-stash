package legacyprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisabledStashImportPreservesCollectionAndMembers(t *testing.T) {
	c := siloCollection{ID: "existing", Title: "Favorite performers", LibraryID: "16", Slug: "javbeacon-stash-preset-20-library-16", Description: collectionOwner}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("disabled import mutated collection: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/api/v2/admin/collections/order" {
			json.NewEncoder(w).Encode(map[string]any{"ordered_ids": []string{c.ID}})
			return
		}
		if r.URL.Path != "/api/v2/admin/collections" {
			t.Errorf("disabled import should not read or reconcile members: %s", r.URL)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []siloCollection{c}})
	}))
	defer server.Close()
	n, complete, err := NewSiloClient(server.URL, "key").SyncCollectionsBatch(context.Background(), []CollectionSpec{{Kind: "library", LibraryID: "16"}}, 400, false, false, true)
	if err != nil || !complete || n != 0 {
		t.Fatalf("changes=%d complete=%t err=%v", n, complete, err)
	}
}

func TestCollectionInventoryReadsEachLibraryOnce(t *testing.T) {
	reads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lib := r.URL.Query().Get("library_id")
		reads[lib]++
		json.NewEncoder(w).Encode(map[string]any{"items": []siloCollection{{ID: lib, LibraryID: lib}}})
	}))
	defer server.Close()
	items, err := NewSiloClient(server.URL, "key").collections(context.Background(), "16", "18", "18", "21")
	if err != nil || len(items) != 3 || reads["16"] != 1 || reads["18"] != 1 || reads["21"] != 1 || reads[""] != 0 {
		t.Fatalf("items=%v reads=%v err=%v", items, reads, err)
	}
}
