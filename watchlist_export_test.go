package main

import (
	"encoding/json"
	"fmt"
	legacy "github.com/Net005/silo-plugin-metadata-stash/internal/legacytasks"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWatchlistLocalFirstDurableRetryAndNewerActionWins(t *testing.T) {
	members := map[string]bool{}
	config := map[string]json.RawMessage{}
	native := false
	remote := false
	fail := true
	mutations := 0
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		row := watchlistCollection{ID: "wl", LibraryID: "16", Title: "Stash | Watchlist", Slug: "javbeacon-stash-preset-7-library-16", Description: "Managed by JAVBeacon metadata plugin.", SourceConfig: config}
		switch r.URL.Path {
		case "/api/v2/watchlist":
			if native {
				fmt.Fprint(w, `{"items":[{"content_id":"local-1","type":"movie"}],"page":{"has_more":false}}`)
			} else {
				fmt.Fprint(w, `{"items":[],"page":{"has_more":false}}`)
			}
		case "/api/v2/admin/collections":
			json.NewEncoder(w).Encode(map[string]any{"items": []watchlistCollection{row}})
		case "/api/v2/admin/collections/wl":
			if r.Method == "PATCH" {
				var input struct {
					Config map[string]json.RawMessage `json:"source_config"`
				}
				json.NewDecoder(r.Body).Decode(&input)
				config = input.Config
			} else {
				json.NewEncoder(w).Encode(row)
			}
		case "/api/v2/admin/collections/wl/items":
			items := []map[string]any{}
			for id := range members {
				items = append(items, map[string]any{"media_item_id": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"items": items})
		case "/api/v2/admin/collections/wl/items/local-1":
			if r.Method == "PUT" {
				members["local-1"] = true
			} else if r.Method == "DELETE" {
				delete(members, "local-1")
			} else {
				t.Error("unexpected local method")
			}
		case "/api/v2/admin/items/local-1/files":
			fmt.Fprint(w, `{"items":[{"library_id":"16","file_path":"/stash/test.mp4"}]}`)
		default:
			t.Errorf("unexpected Silo request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer silo.Close()
	stash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "findSavedFilters"):
			fmt.Fprint(w, `{"data":{"findSavedFilters":[{"id":"7","name":"Watchlist","object_filter":{}}]}}`)
		case strings.Contains(req.Query, "findScenes"):
			if req.Variables["path"] != nil {
				fmt.Fprint(w, `{"data":{"findScenes":{"count":1,"scenes":[{"id":"42","files":[{"path":"/stash/test.mp4"}]}]}}}`)
			} else {
				fmt.Fprint(w, `{"data":{"findScenes":{"count":0,"scenes":[]}}}`)
			}
		case strings.Contains(req.Query, "configuration"):
			fmt.Fprint(w, `{"data":{"configuration":{"plugins":{"stash-silo-companion":{"watchlist_tag_id":"1355"}}}}}`)
		case strings.Contains(req.Query, "findScene"):
			tags := []map[string]string{{"id": "other", "name": "Keep"}}
			if remote {
				tags = append(tags, map[string]string{"id": "1355", "name": "Watchlist"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"findScene": map[string]any{"id": "42", "tags": tags}}})
		case strings.Contains(req.Query, "bulkSceneUpdate"):
			input := req.Variables["input"].(map[string]any)
			if len(input) != 2 {
				t.Error("mutation changes more than tag membership")
			}
			desired := input["tag_ids"].(map[string]any)["mode"] == "ADD"
			if members["local-1"] != desired {
				t.Error("Stash written before local collection")
			}
			if fail {
				http.Error(w, "offline", 503)
				return
			}
			remote = desired
			mutations++
			fmt.Fprint(w, `{"data":{"bulkSceneUpdate":[{"id":"42"}]}}`)
		default:
			t.Errorf("unexpected GraphQL %s", req.Query)
		}
	}))
	defer stash.Close()
	manager := legacy.New(hclog.NewNullLogger())
	manager.Provider().ConfigureStashConnection(stash.URL, "key")
	newRuntime := func() *runtimeServer {
		return &runtimeServer{siloBase: silo.URL, siloKey: "key", stashFilters: "Watchlist", stashPrefix: "Stash | ", client: &stashClient{base: stash.URL, key: "key"}, legacy: manager}
	}
	rt := newRuntime()
	if _, e := rt.backfillWatchlist(t.Context()); e != nil {
		t.Fatal(e)
	}
	event := func(op pluginv1.WatchSyncOperation, at time.Time) *pluginv1.WatchSyncEvent {
		return &pluginv1.WatchSyncEvent{Operation: op, OccurredAt: timestamppb.New(at), Media: &pluginv1.WatchSyncMedia{MediaItemId: "local-1", ExternalIds: map[string]string{"stash": "42"}}}
	}
	now := time.Now()
	add := event(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST, now)
	if e := rt.applyWatchlistEvent(t.Context(), add); e == nil {
		t.Fatal("offline export acknowledged")
	}
	if !members["local-1"] || remote {
		t.Fatal("local-first failed")
	}
	var journal watchlistJournal
	json.Unmarshal(config[watchlistJournalKey], &journal)
	if len(journal.Pending) != 1 {
		t.Fatal("failed intent not durable")
	}
	rt = newRuntime()
	fail = false
	if n, e := rt.backfillWatchlist(t.Context()); e != nil || n != 1 || !remote {
		t.Fatalf("restart retry %d %v", n, e)
	}
	remove := event(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_REMOVE_FROM_WATCHLIST, now.Add(time.Second))
	if e := rt.applyWatchlistEvent(t.Context(), remove); e != nil {
		t.Fatal(e)
	}
	if e := rt.applyWatchlistEvent(t.Context(), add); e != nil {
		t.Fatal(e)
	}
	if remote || members["local-1"] {
		t.Fatal("older retry undid removal")
	}
	if mutations != 2 {
		t.Fatalf("duplicate mutations %d", mutations)
	}
	// Direct collection edits are detected without a watch-provider event.
	members["local-1"] = true
	if _, e := rt.backfillWatchlist(t.Context()); e != nil || !remote {
		t.Fatalf("manual collection add %v", e)
	}
	delete(members, "local-1")
	if _, e := rt.backfillWatchlist(t.Context()); e != nil || remote {
		t.Fatalf("manual collection removal %v", e)
	}
	// Native items without IMDb/TMDB IDs are still recovered from local changes.
	native = true
	if _, e := rt.backfillWatchlist(t.Context()); e != nil || !remote || !members["local-1"] {
		t.Fatalf("native add not exported: %v", e)
	}
	native = false
	if _, e := rt.backfillWatchlist(t.Context()); e != nil || remote || members["local-1"] {
		t.Fatalf("native removal not exported: %v", e)
	}

}
