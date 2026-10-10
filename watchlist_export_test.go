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
	newerHead := false
	nativeAdded := time.Now().UTC()
	shared := false
	remote := false
	fail := true
	mutations := 0
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1"`)
		row := watchlistCollection{ID: "wl", LibraryID: "16", Title: "Stash | Watchlist", Slug: "javbeacon-stash-preset-7-library-16", Description: "Managed by JAVBeacon metadata plugin.", SourceConfig: config}
		switch r.URL.Path {
		case "/api/v2/admin/items/newer/files":
			fmt.Fprint(w, `{"items":[]}`)
		case "/api/v2/watchlist/newer":
			json.NewEncoder(w).Encode(map[string]any{"added_at": nativeAdded.Add(time.Second)})
		case "/api/v2/watchlist/local-1":
			json.NewEncoder(w).Encode(map[string]any{"added_at": nativeAdded})
		case "/api/v2/watchlist":
			if native && newerHead {
				fmt.Fprint(w, `{"items":[{"content_id":"newer","type":"movie"},{"content_id":"local-1","type":"movie"}],"page":{"has_more":false}}`)
			} else if native {
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
		case "/api/v2/admin/collections/wl/items/order":
			if r.Method == "GET" {
				ids := []string{}
				for id := range members {
					ids = append(ids, id)
				}
				json.NewEncoder(w).Encode(map[string]any{"ordered_ids": ids})
			} else {
				w.WriteHeader(204)
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
			if shared {
				fmt.Fprint(w, `{"items":[{"library_id":"16","file_path":"/stash/test.mp4"},{"library_id":"3","file_path":"/movies/same-title.mkv"}]}`)
			} else {
				fmt.Fprint(w, `{"items":[{"library_id":"16","file_path":"/stash/test.mp4"}]}`)
			}
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
			if path, ok := req.Variables["path"].(map[string]any); ok && path["value"] == "/stash/test.mp4" {
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
	// A rapid remove/add can retain native membership across polls.
	nativeAdded = nativeAdded.Add(time.Second)
	if _, e := rt.backfillWatchlist(t.Context()); e != nil {
		t.Fatal(e)
	}
	var recovered watchlistJournal
	json.Unmarshal(config[watchlistJournalKey], &recovered)
	if !recovered.NativeHeadAddedAt.Equal(nativeAdded) || !recovered.LastActions["local-1"].Desired {
		t.Fatal("rapid re-add was not detected")
	}
	// A later addition of another title must not hide an older missed re-add.
	recovered.LastActions["local-1"] = watchlistActionVersion{Changed: nativeAdded.Add(-time.Second), Desired: false}
	delete(recovered.Baseline, "local-1")
	delete(members, "local-1")
	remote = false
	config[watchlistJournalKey], _ = json.Marshal(recovered)
	newerHead = true
	if _, err := rt.backfillWatchlist(t.Context()); err != nil || !members["local-1"] || !remote {
		t.Fatalf("non-head re-add lost: %v", err)
	}
	newerHead = false
	native = false
	if _, e := rt.backfillWatchlist(t.Context()); e != nil || remote || members["local-1"] {
		t.Fatalf("native removal not exported: %v", e)
	}

	// A regular movie sharing the Silo identity must not affect either the
	// Stash tag or the library collection, even when event IDs claim Stash.
	shared = true
	before := mutations
	native = true
	if _, err := rt.backfillWatchlist(t.Context()); err != nil {
		t.Fatal(err)
	}
	if remote || members["local-1"] || mutations != before {
		t.Fatal("shared movie native Watchlist leaked into Stash")
	}
	if err := rt.applyWatchlistEvent(t.Context(), event(pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_ADD_TO_WATCHLIST, now.Add(10*time.Second))); err != errOutsideStashWatchlist {
		t.Fatalf("shared identity accepted: %v", err)
	}
	if remote || members["local-1"] || mutations != before {
		t.Fatal("shared movie event leaked into Stash")
	}

}

func TestWatchlistAddMovesExistingMemberFirstAndPreservesOthers(t *testing.T) {
	ids := []string{"older", "target", "other"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"order-1"`)
		switch r.URL.Path {
		case "/api/v2/admin/collections/wl/items":
			items := []map[string]string{}
			pageIDs := ids[:2]
			if r.URL.Query().Get("cursor") == "tail" {
				pageIDs = ids[2:]
			}
			for _, id := range pageIDs {
				items = append(items, map[string]string{"media_item_id": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"items": items, "page": map[string]any{"has_more": r.URL.Query().Get("cursor") == "", "next_cursor": "tail"}})
		case "/api/v2/admin/collections/wl/items/order":
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]any{"ordered_ids": ids[:1], "has_more": true})
			} else {
				if r.Header.Get("If-Match") != `"order-1"` {
					t.Error("missing order CAS")
				}
				var p struct {
					IDs []string `json:"ordered_ids"`
				}
				json.NewDecoder(r.Body).Decode(&p)
				ids = p.IDs
			}
		default:
			t.Errorf("unexpected membership mutation %s", r.URL.Path)
		}
	}))
	defer server.Close()
	rt := &runtimeServer{siloBase: server.URL, siloKey: "key"}
	if err := rt.applyLocalWatchlist(t.Context(), "wl", "target", true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "target,older,other" {
		t.Fatalf("incorrect order %v", ids)
	}
	if err := rt.applyLocalWatchlist(t.Context(), "wl", "target", true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "target,older,other" {
		t.Fatal("retry changed order")
	}
}
