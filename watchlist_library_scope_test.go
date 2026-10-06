package main

import (
	"encoding/json"
	"fmt"
	legacy "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	"github.com/hashicorp/go-hclog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWatchlistResolveExcludesOrdinaryMovieFilesAndPagesSelectedLibrary(t *testing.T) {
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "second" {
			fmt.Fprint(w, `{"items":[{"library_id":"21","file_path":"/stash/scene.mp4"},{"library_id":"19","file_path":"/another/unrelated.mp4"}],"page":{"has_more":false}}`)
		} else {
			fmt.Fprint(w, `{"items":[{"library_id":"3","file_path":"/movies/unrelated.mkv"}],"page":{"has_more":true,"next_cursor":"second"}}`)
		}
	}))
	defer silo.Close()
	calls := 0
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		path := req.Variables["path"].(map[string]any)["value"]
		if path != "/stash/scene.mp4" {
			t.Errorf("queried an unrelated library path: %v", path)
		}
		calls++
		fmt.Fprint(w, `{"data":{"findScenes":{"count":1,"scenes":[{"id":"42167","files":[{"path":"/stash/scene.mp4"}]}]}}}`)
	}))
	defer stash.Close()
	rt := &runtimeServer{siloBase: silo.URL, siloKey: "key", client: &stashClient{base: stash.URL, key: "key"}}
	id, err := rt.resolveWatchlistScene(t.Context(), "movie-shared", "21")
	if err != nil || id != "42167" || calls != 1 {
		t.Fatalf("id=%s calls=%d err=%v", id, calls, err)
	}
	id, err = rt.resolveWatchlistScene(t.Context(), "movie-shared", "16")
	if err == nil || id != "" || calls != 1 {
		t.Fatal("missing selected-library file guessed a Stash identity")
	}
}

func TestWatchlistParksMissingLibraryIntentAndContinues(t *testing.T) {
	journal := watchlistJournal{Version: 1, NativeSeeded: true, Baseline: map[string]bool{"gone": true, "valid": true}, Pending: map[string]watchlistIntent{"gone": {Desired: true}, "valid": {Desired: true}}}
	config := map[string]json.RawMessage{}
	config[watchlistJournalKey], _ = json.Marshal(journal)
	back := false
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"one"`)
		switch r.URL.Path {
		case "/api/v2/admin/collections":
			fmt.Fprint(w, `{"items":[{"id":"wl","library_id":"16","title":"Stash | Watchlist","slug":"javbeacon-stash-preset-7-library-16","description":"Managed by JAVBeacon metadata plugin."}]}`)
		case "/api/v2/watchlist":
			fmt.Fprint(w, `{"items":[]}`)
		case "/api/v2/admin/collections/wl":
			if r.Method == "PATCH" {
				var x struct {
					SourceConfig map[string]json.RawMessage `json:"source_config"`
				}
				json.NewDecoder(r.Body).Decode(&x)
				config = x.SourceConfig
			} else {
				json.NewEncoder(w).Encode(watchlistCollection{ID: "wl", LibraryID: "16", SourceConfig: config})
			}
		case "/api/v2/admin/collections/wl/items":
			fmt.Fprint(w, `{"items":[{"media_item_id":"gone"},{"media_item_id":"valid"}]}`)
		case "/api/v2/admin/items/gone/files":
			if back {
				fmt.Fprint(w, `{"items":[{"library_id":"16","file_path":"/stash/scene.mp4"}]}`)
			} else {
				fmt.Fprint(w, `{"items":[{"library_id":"3","file_path":"/movies/unrelated.mkv"}]}`)
			}
		case "/api/v2/admin/items/valid/files":
			fmt.Fprint(w, `{"items":[{"library_id":"16","file_path":"/stash/scene.mp4"}]}`)
		default:
			t.Errorf("unexpected Silo request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer silo.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&q)
		switch {
		case strings.Contains(q.Query, "findSavedFilters"):
			fmt.Fprint(w, `{"data":{"findSavedFilters":[{"id":"7","name":"Watchlist","object_filter":{}}]}}`)
		case strings.Contains(q.Query, "findScenes"):
			fmt.Fprint(w, `{"data":{"findScenes":{"count":1,"scenes":[{"id":"42","files":[{"path":"/stash/scene.mp4"}]}]}}}`)
		case strings.Contains(q.Query, "configuration"):
			fmt.Fprint(w, `{"data":{"configuration":{"plugins":{"stash-silo-companion":{"watchlist_tag_id":"1355"}}}}}`)
		case strings.Contains(q.Query, "findScene"):
			fmt.Fprint(w, `{"data":{"findScene":{"id":"42","tags":[{"id":"1355"}]}}}`)
		default:
			t.Error("unexpected Stash mutation/query")
			http.Error(w, "unexpected", 500)
		}
	}))
	defer stash.Close()
	manager := legacy.New(hclog.NewNullLogger())
	manager.Provider().ConfigureStashConnection(stash.URL, "key")
	rt := &runtimeServer{siloBase: silo.URL, siloKey: "key", client: &stashClient{base: stash.URL, key: "key"}, legacy: manager, stashFilters: "Watchlist", stashPrefix: "Stash | "}
	n, err := rt.backfillWatchlist(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("processed=%d err=%v", n, err)
	}
	journal = watchlistJournal{}
	json.Unmarshal(config[watchlistJournalKey], &journal)
	if len(journal.Pending) != 0 || !journal.Inactive["gone"].Desired {
		t.Fatal("missing-library intent not preserved")
	}
	back = true
	n, err = rt.backfillWatchlist(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("restored processed=%d err=%v", n, err)
	}
	journal = watchlistJournal{}
	json.Unmarshal(config[watchlistJournalKey], &journal)
	if len(journal.Pending) != 0 || len(journal.Inactive) != 0 {
		t.Fatal("restored intent not acknowledged")
	}
}
